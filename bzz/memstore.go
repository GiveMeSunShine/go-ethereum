// memory storage layer for the package blockhash

package bzz

import (
	"bytes"
)

const (
	maxEntries = 500 // max number of stored (cached) blocks
	memTreeLW  = 2   // log2(subtree count) of the subtrees
	memTreeFLW = 14  // log2(subtree count) of the root layer
)

type dpaMemStorage struct {
	memtree    *dpaMemTree
	entry_cnt  uint   // stored entries
	access_cnt uint64 // access counter; oldest is thrown away when full
}

/*
a hash prefix subtree containing subtrees or one storage entry (but never both)

- access[0] stores the smallest (oldest) access count value in this subtree
- if it contains more subtrees and its subtree count is at least 4, access[1:2]
  stores the smallest access count in the first and second halves of subtrees
  (so that access[0] = min(access[1], access[2])
- likewise, if subtree count is at least 8,
  access[1] = min(access[3], access[4])
  access[2] = min(access[5], access[6])
  (access[] is a binary tree inside the multi-bit leveled hash tree)
*/

func (x Key) Size() uint {
	return uint(len(x))
}

func (x Key) isEqual(y Key) bool {
	return bytes.Compare(x, y) == 0
}

func (h Key) bits(i, j uint) uint {

	ii := i >> 3
	jj := i & 7
	if ii >= h.Size() {
		return 0
	}

	if jj+j <= 8 {
		return uint((h[ii] >> jj) & ((1 << j) - 1))
	}

	res := uint(h[ii] >> jj)
	jj = 8 - jj
	j -= jj
	for j != 0 {
		ii++
		if j < 8 {
			res += uint(h[ii]&((1<<j)-1)) << jj
			return res
		}
		res += uint(h[ii]) << jj
		jj += 8
		j -= 8
	}
	return res
}

type dpaMemTree struct {
	subtree    []*dpaMemTree
	parent     *dpaMemTree
	parent_idx uint

	bits  uint // log2(subtree count)
	width uint // subtree count

	entry  *Chunk // if subtrees are present, entry should be nil
	access []uint64
}

func newTreeNode(b uint, parent *dpaMemTree, pidx uint) (node *dpaMemTree) {

	node = new(dpaMemTree)
	node.bits = b
	node.width = 1 << uint(b)
	node.subtree = make([]*dpaMemTree, node.width)
	node.access = make([]uint64, node.width-1)
	node.parent = parent
	node.parent_idx = pidx
	if parent != nil {
		parent.subtree[pidx] = node
	}

	return node

}

func (node *dpaMemTree) update_access(a uint64) {

	aidx := uint(0)
	var aa uint64
	oa := node.access[0]
	for node.access[aidx] == oa {
		node.access[aidx] = a
		if aidx > 0 {
			aa = node.access[((aidx-1)^1)+1]
			aidx = (aidx - 1) >> 1
		} else {
			pidx := node.parent_idx
			node = node.parent
			if node == nil {
				return
			}
			nn := node.subtree[pidx^1]
			if nn != nil {
				aa = nn.access[0]
			} else {
				aa = 0
			}
			aidx = (node.width + pidx - 2) >> 1
		}

		if (aa != 0) && (aa < a) {
			a = aa
		}
	}

}

func (s *dpaMemStorage) add(entry *Chunk) {

	s.access_cnt++

	node := s.memtree
	bitpos := uint(0)
	for node.entry == nil {
		l := entry.Key.bits(bitpos, node.bits)
		st := node.subtree[l]
		if st == nil {
			st = newTreeNode(memTreeLW, node, l)
			bitpos += node.bits
			node = st
			break
		}
		bitpos += node.bits
		node = st
	}

	if node.entry != nil {

		if node.entry.Key.isEqual(entry.Key) {
			node.update_access(s.access_cnt)
			return
		}

		for node.entry != nil {

			l := node.entry.Key.bits(bitpos, node.bits)
			st := node.subtree[l]
			if st == nil {
				st = newTreeNode(memTreeLW, node, l)
			}
			st.entry = node.entry
			node.entry = nil
			st.update_access(node.access[0])

			l = entry.Key.bits(bitpos, node.bits)
			st = node.subtree[l]
			if st == nil {
				st = newTreeNode(memTreeLW, node, l)
			}
			bitpos += node.bits
			node = st

		}
	}

	node.entry = entry
	node.update_access(s.access_cnt)
	s.entry_cnt++

}

func (s *dpaMemStorage) find(hash Key) (entry *Chunk) {

	node := s.memtree
	bitpos := uint(0)
	for node.entry == nil {
		l := hash.bits(bitpos, node.bits)
		st := node.subtree[l]
		if st == nil {
			return nil
		}
		bitpos += node.bits
		node = st
	}

	if node.entry.Key.isEqual(hash) {
		s.access_cnt++
		node.update_access(s.access_cnt)
		return node.entry
	} else {
		return nil
	}
}

func (s *dpaMemStorage) remove_oldest() {

	node := s.memtree

	for node.entry == nil {

		aidx := uint(0)
		av := node.access[aidx]

		for aidx < node.width/2-1 {
			if av == node.access[aidx*2+1] {
				node.access[aidx] = node.access[aidx*2+2]
				aidx = aidx*2 + 1
			} else if av == node.access[aidx*2+2] {
				node.access[aidx] = node.access[aidx*2+1]
				aidx = aidx*2 + 2
			} else {
				panic(nil)
			}
		}
		pidx := aidx*2 + 2 - node.width
		if (node.subtree[pidx] != nil) && (av == node.subtree[pidx].access[0]) {
			if node.subtree[pidx+1] != nil {
				node.access[aidx] = node.subtree[pidx+1].access[0]
			} else {
				node.access[aidx] = 0
			}
		} else if (node.subtree[pidx+1] != nil) && (av == node.subtree[pidx+1].access[0]) {
			if node.subtree[pidx] != nil {
				node.access[aidx] = node.subtree[pidx].access[0]
			} else {
				node.access[aidx] = 0
			}
			pidx++
		} else {
			panic(nil)
		}

		//fmt.Println(pidx)
		node = node.subtree[pidx]

	}

	node.entry = nil
	s.entry_cnt--
	node.access[0] = 0

	//---

	aidx := uint(0)
	for {
		aa := node.access[aidx]
		if aidx > 0 {
			aidx = (aidx - 1) >> 1
		} else {
			pidx := node.parent_idx
			node = node.parent
			if node == nil {
				return
			}
			aidx = (node.width + pidx - 2) >> 1
		}
		if (aa != 0) && ((aa < node.access[aidx]) || (node.access[aidx] == 0)) {
			node.access[aidx] = aa
		}
	}

}

func (s *dpaMemStorage) Put(req *Chunk) error {
	if s.entry_cnt >= maxEntries {
		s.remove_oldest()
	}
	s.add(req)
	return nil
}

// process retrieve channel requests

func (s *dpaMemStorage) Get(req *Chunk) {

	entry := s.find(req.Key)
	if entry == nil {
	}

}

func (s *dpaMemStorage) Init() {

	s.memtree = newTreeNode(memTreeFLW, nil, 0)

}
