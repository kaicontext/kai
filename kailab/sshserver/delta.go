package sshserver

// Delta encoding for Git pack files.
// Based on the algorithm used by JGit and go-git.

const (
	// Minimum match size to consider for a copy instruction
	minMatchSize = 4
	// Block size for hash indexing
	blockSize = 16
	// Maximum chain length to prevent quadratic behavior
	maxChainLength = 64
)

// deltaIndex builds a hash table of the source data for fast lookups.
type deltaIndex struct {
	src   []byte
	table []int // hash -> first offset, -1 if empty
	next  []int // offset -> next offset with same hash, -1 if none
	mask  int
}

// newDeltaIndex creates a delta index for the source data.
func newDeltaIndex(src []byte) *deltaIndex {
	if len(src) < blockSize {
		return &deltaIndex{src: src}
	}

	// Size table as power of 2
	tableSize := 1
	for tableSize < len(src)/blockSize {
		tableSize <<= 1
	}
	if tableSize < 256 {
		tableSize = 256
	}

	idx := &deltaIndex{
		src:   src,
		table: make([]int, tableSize),
		next:  make([]int, len(src)-blockSize+1),
		mask:  tableSize - 1,
	}

	// Initialize table with -1 (no entry)
	for i := range idx.table {
		idx.table[i] = -1
	}
	for i := range idx.next {
		idx.next[i] = -1
	}

	// Build index - scan source in 16-byte blocks
	for i := 0; i <= len(src)-blockSize; i++ {
		h := hashBlock(src[i:])
		tIdx := h & idx.mask
		idx.next[i] = idx.table[tIdx]
		idx.table[tIdx] = i
	}

	return idx
}

// hashBlock computes a hash of a 16-byte block.
func hashBlock(data []byte) int {
	// Simple rolling hash
	h := 0
	for i := 0; i < blockSize && i < len(data); i++ {
		h = h*31 + int(data[i])
	}
	return h & 0x7FFFFFFF
}

// findMatch finds the longest match at the given target offset.
// Returns (srcOffset, matchLength) or (-1, 0) if no match found.
func (idx *deltaIndex) findMatch(tgt []byte, tgtOff int) (int, int) {
	if idx.table == nil || tgtOff+blockSize > len(tgt) {
		return -1, 0
	}

	h := hashBlock(tgt[tgtOff:])
	srcOff := idx.table[h&idx.mask]

	bestSrcOff := -1
	bestLen := 0
	chainLen := 0

	for srcOff >= 0 && chainLen < maxChainLength {
		matchLen := idx.matchLength(srcOff, tgt, tgtOff)
		if matchLen > bestLen {
			bestLen = matchLen
			bestSrcOff = srcOff
		}
		srcOff = idx.next[srcOff]
		chainLen++
	}

	if bestLen < minMatchSize {
		return -1, 0
	}
	return bestSrcOff, bestLen
}

// matchLength computes how many bytes match starting at the given offsets.
func (idx *deltaIndex) matchLength(srcOff int, tgt []byte, tgtOff int) int {
	maxLen := len(idx.src) - srcOff
	if len(tgt)-tgtOff < maxLen {
		maxLen = len(tgt) - tgtOff
	}

	length := 0
	for length < maxLen && idx.src[srcOff+length] == tgt[tgtOff+length] {
		length++
	}
	return length
}

// GenerateDelta creates a delta that transforms src into tgt.
// Returns nil if delta would not provide meaningful compression.
func GenerateDelta(src, tgt []byte) []byte {
	// Don't bother with very small objects
	if len(tgt) < 16 {
		return nil
	}

	// Check size ratio - target shouldn't be too much smaller than source
	if len(src) > 0 && len(tgt) < len(src)/16 {
		return nil
	}

	idx := newDeltaIndex(src)
	if idx.table == nil {
		return nil
	}

	// Build delta
	var delta []byte

	// Header: source and target sizes (variable-length encoded)
	delta = append(delta, deltaEncodeSize(len(src))...)
	delta = append(delta, deltaEncodeSize(len(tgt))...)

	// Buffer for insert data
	insertBuf := make([]byte, 0, 127)

	tgtOff := 0
	for tgtOff < len(tgt) {
		srcOff, matchLen := idx.findMatch(tgt, tgtOff)

		if matchLen >= minMatchSize {
			// Flush any pending inserts
			if len(insertBuf) > 0 {
				delta = append(delta, encodeInsert(insertBuf)...)
				insertBuf = insertBuf[:0]
			}

			// Encode copy instruction
			delta = append(delta, encodeCopy(srcOff, matchLen)...)
			tgtOff += matchLen
		} else {
			// Add to insert buffer
			insertBuf = append(insertBuf, tgt[tgtOff])
			tgtOff++

			// Flush if buffer is full
			if len(insertBuf) == 127 {
				delta = append(delta, encodeInsert(insertBuf)...)
				insertBuf = insertBuf[:0]
			}
		}
	}

	// Flush remaining inserts
	if len(insertBuf) > 0 {
		delta = append(delta, encodeInsert(insertBuf)...)
	}

	// Check if delta is actually smaller
	// Add some overhead for REF_DELTA header (20 bytes for base OID)
	if len(delta)+20 >= len(tgt) {
		return nil
	}

	return delta
}

// deltaEncodeSize encodes a size as variable-length integer.
func deltaEncodeSize(size int) []byte {
	var buf []byte
	c := size & 0x7f
	size >>= 7
	for size > 0 {
		buf = append(buf, byte(c|0x80))
		c = size & 0x7f
		size >>= 7
	}
	buf = append(buf, byte(c))
	return buf
}

// encodeInsert encodes an insert instruction.
// Format: length byte (1-127) followed by literal data.
func encodeInsert(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	result := make([]byte, 0, len(data)+1)
	for len(data) > 0 {
		n := len(data)
		if n > 127 {
			n = 127
		}
		result = append(result, byte(n))
		result = append(result, data[:n]...)
		data = data[n:]
	}
	return result
}

// encodeCopy encodes a copy instruction.
// Format: command byte with flags, followed by offset/size bytes.
func encodeCopy(offset, size int) []byte {
	var buf []byte
	cmd := byte(0x80) // Copy instruction flag

	// Encode offset (up to 4 bytes)
	if offset&0xff != 0 {
		cmd |= 0x01
	}
	if offset&0xff00 != 0 {
		cmd |= 0x02
	}
	if offset&0xff0000 != 0 {
		cmd |= 0x04
	}
	if offset&0xff000000 != 0 {
		cmd |= 0x08
	}

	// Encode size (up to 3 bytes, 0 means 0x10000)
	if size != 0x10000 {
		if size&0xff != 0 {
			cmd |= 0x10
		}
		if size&0xff00 != 0 {
			cmd |= 0x20
		}
		if size&0xff0000 != 0 {
			cmd |= 0x40
		}
	}

	buf = append(buf, cmd)

	// Write offset bytes
	if cmd&0x01 != 0 {
		buf = append(buf, byte(offset))
	}
	if cmd&0x02 != 0 {
		buf = append(buf, byte(offset>>8))
	}
	if cmd&0x04 != 0 {
		buf = append(buf, byte(offset>>16))
	}
	if cmd&0x08 != 0 {
		buf = append(buf, byte(offset>>24))
	}

	// Write size bytes
	if cmd&0x10 != 0 {
		buf = append(buf, byte(size))
	}
	if cmd&0x20 != 0 {
		buf = append(buf, byte(size>>8))
	}
	if cmd&0x40 != 0 {
		buf = append(buf, byte(size>>16))
	}

	return buf
}
