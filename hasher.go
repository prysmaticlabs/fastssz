package ssz

import (
	"encoding/binary"
	"fmt"
	"hash"
	"math/bits"
	"sync"

	"github.com/minio/sha256-simd"
	"github.com/prysmaticlabs/gohashtree"
)

var (
	// ErrIncorrectByteSize means that the byte size is incorrect
	ErrIncorrectByteSize = fmt.Errorf("incorrect byte size")

	// ErrIncorrectListSize means that the size of the list is incorrect
	ErrIncorrectListSize = fmt.Errorf("incorrect list size")
)

var zeroHashes [65][32]byte
var zeroHashLevels map[string]int
var trueBytes, falseBytes [32]byte

const (
	mask0 = ^uint64((1 << (1 << iota)) - 1)
	mask1
	mask2
	mask3
	mask4
	mask5
)

const (
	bit0 = uint8(1 << iota)
	bit1
	bit2
	bit3
	bit4
	bit5
)

func init() {
	trueBytes[0] = 1
	zeroHashLevels = make(map[string]int)
	zeroHashLevels[string(falseBytes[:])] = 0

	tmp := [64]byte{}
	for i := 0; i < 64; i++ {
		copy(tmp[:32], zeroHashes[i][:])
		copy(tmp[32:], zeroHashes[i][:])
		zeroHashes[i+1] = sha256.Sum256(tmp[:])
		zeroHashLevels[string(zeroHashes[i+1][:])] = i + 1
	}
}

// HashWithDefaultHasher hashes a HashRoot object with a Hasher from
// the default HasherPool
func HashWithDefaultHasher(v HashRoot) ([32]byte, error) {
	hh := DefaultHasherPool.Get()
	if err := v.HashTreeRootWith(hh); err != nil {
		DefaultHasherPool.Put(hh)
		return [32]byte{}, err
	}
	root := hh.HashRoot()
	DefaultHasherPool.Put(hh)
	return root, nil
}

var zeroBytes = make([]byte, 32)

// DefaultHasherPool is a default hasher pool
var DefaultHasherPool HasherPool

// Hasher is a utility tool to hash SSZ structs
type Hasher struct {
	// buffer array to store hashing values
	buf [][32]byte

	pos uint8

	// tmp array used for uint64 and bitlist processing
	tmp []byte

	// tmp array used during the merkleize process
	merkleizeTmp []byte

	// sha256 hash function
	hash hash.Hash
}

// NewHasher creates a new Hasher object
func NewHasher() *Hasher {
	return &Hasher{
		hash: sha256.New(),
		tmp:  make([]byte, 32),
	}
}

// NewHasher creates a new Hasher object with a custom hash function
func NewHasherWithHash(hh hash.Hash) *Hasher {
	return &Hasher{
		hash: hh,
		tmp:  make([]byte, 32),
	}
}

// Reset resets the Hasher obj
func (h *Hasher) Reset() {
	h.buf = h.buf[:0]
	h.hash.Reset()
}

func (h *Hasher) AppendBytes32(b []byte) {
	var b32 [32]byte
	copy(b32[:], b)
	h.buf = append(h.buf, b32)
}

// PutUint64 appends a uint64 in 32 bytes
func (h *Hasher) PutUint64(i uint64) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, i)
	h.AppendBytes32(buf)
}

// PutUint32 appends a uint32 in 32 bytes
func (h *Hasher) PutUint32(i uint32) {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, i)
	h.AppendBytes32(buf)
}

// PutUint16 appends a uint16 in 32 bytes
func (h *Hasher) PutUint16(i uint16) {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, i)
	h.AppendBytes32(buf)
}

// PutUint16 appends a uint16 in 32 bytes
func (h *Hasher) PutUint8(i uint8) {
	h.AppendBytes32([]byte{byte(i)})
}

func CalculateLimit(maxCapacity, numItems, size uint64) uint64 {
	limit := (maxCapacity*size + 31) / 32
	if limit != 0 {
		return limit
	}
	if numItems == 0 {
		return 1
	}
	return numItems
}

func (h *Hasher) FillUpTo32() {
	// pad zero bytes to the left
	if h.pos != 0 {
		copy(h.buf[len(h.buf)-1][h.pos:], zeroBytes[:32-h.pos])
	}
	h.pos = 0
}

func (h *Hasher) AppendUint8(i uint8) {
	dst := make([]byte, 0)
	dst = MarshalUint8(dst, i)
	if h.pos == 0 {
		var b32 [32]byte
		copy(b32[:], dst)
		h.buf = append(h.buf, b32)
	} else {
		copy(h.buf[len(h.buf)-1][h.pos:], dst)
	}
	h.pos = (h.pos + 1) % 32
}

func (h *Hasher) AppendUint64(i uint64) {
	dst := make([]byte, 0)
	dst = MarshalUint64(dst, i)
	willFit := h.pos <= 24
	if willFit {
		copy(h.buf[len(h.buf)-1][h.pos:], dst)
	} else {
		copy(h.buf[len(h.buf)-1][h.pos:], dst[:32-h.pos])
		var b32 [32]byte
		copy(b32[:], dst[32-h.pos:])
		h.buf = append(h.buf, b32)
	}
	h.pos = (h.pos + 8) % 32
}

func (h *Hasher) Append(i [32]byte) {
	h.buf = append(h.buf, i)
}

// PutRootVector appends an array of roots
func (h *Hasher) PutRootVector(b [][]byte, maxCapacity ...uint64) error {
	indx := h.Index()
	for _, i := range b {
		if len(i) != 32 {
			return fmt.Errorf("bad root")
		}
		var b32 [32]byte
		copy(b32[:], i)
		h.buf = append(h.buf, b32)
	}

	if len(maxCapacity) == 0 {
		h.Merkleize(indx)
	} else {
		numItems := uint64(len(b))
		limit := CalculateLimit(maxCapacity[0], numItems, 32)

		h.MerkleizeWithMixin(indx, numItems, limit)
	}
	return nil
}

// PutUint64Array appends an array of uint64
func (h *Hasher) PutUint64Array(b []uint64, maxCapacity ...uint64) {
	indx := h.Index()
	for _, i := range b {
		h.AppendUint64(i)
	}

	// pad zero bytes to the left
	h.FillUpTo32()

	if len(maxCapacity) == 0 {
		// Array with fixed size
		h.Merkleize(indx)
	} else {
		numItems := uint64(len(b))
		limit := CalculateLimit(maxCapacity[0], numItems, 8)

		h.MerkleizeWithMixin(indx, numItems, limit)
	}
}

func parseBitlist(dst, buf []byte) ([]byte, uint64) {
	msb := uint8(bits.Len8(buf[len(buf)-1])) - 1
	size := uint64(8*(len(buf)-1) + int(msb))

	dst = append(dst, buf...)
	dst[len(dst)-1] &^= uint8(1 << msb)

	newLen := len(dst)
	for i := len(dst) - 1; i >= 0; i-- {
		if dst[i] != 0x00 {
			break
		}
		newLen = i
	}
	res := dst[:newLen]
	return res, size
}

// PutBitlist appends a ssz bitlist
func (h *Hasher) PutBitlist(bb []byte, maxSize uint64) {
	var size uint64
	h.tmp, size = parseBitlist(h.tmp[:0], bb)

	// merkleize the content with mix in length
	indx := h.Index()
	h.AppendBytes32(h.tmp)
	h.MerkleizeWithMixin(indx, size, (maxSize+255)/256)
}

// PutBool appends a boolean
func (h *Hasher) PutBool(b bool) {
	if b {
		h.buf = append(h.buf, trueBytes)
	} else {
		h.buf = append(h.buf, falseBytes)
	}
}

// PutBytes appends bytes
func (h *Hasher) PutBytes(b []byte) {
	if len(b) <= 32 {
		h.AppendBytes32(b)
		return
	}

	// if the bytes are longer than 32 we have to
	// merkleize the content
	indx := h.Index()
	h.AppendBytes32(b)
	h.Merkleize(indx)
}

// Index marks the current buffer index
func (h *Hasher) Index() int {
	return len(h.buf)
}

// Merkleize is used to merkleize the last group of the hasher
func (h *Hasher) Merkleize(indx int) {
	input := h.buf[indx:]
	result := merkleizeInput(input, 0)
	h.buf = append(h.buf[:indx], result)
}

// MerkleizeWithMixin is used to merkleize the last group of the hasher
func (h *Hasher) MerkleizeWithMixin(indx int, num, limit uint64) {
	input := h.buf[indx:]
	result := merkleizeInput(input, limit)

	// mix in with the size
	output := h.tmp[:32]
	for indx := range output {
		output[indx] = 0
	}
	MarshalUint64(output[:0], num)
	h.doHash(result[:], result[:], output)
	h.buf = append(h.buf[:indx], result)
}

// HashRoot creates the hash final hash root
func (h *Hasher) HashRoot() [32]byte {
	return h.buf[0]
}

// HasherPool may be used for pooling Hashers for similarly typed SSZs.
type HasherPool struct {
	pool sync.Pool
}

// Get acquires a Hasher from the pool.
func (hh *HasherPool) Get() *Hasher {
	h := hh.pool.Get()
	if h == nil {
		return NewHasher()
	}
	return h.(*Hasher)
}

// Put releases the Hasher to the pool.
func (hh *HasherPool) Put(h *Hasher) {
	h.Reset()
	hh.pool.Put(h)
}

func nextPowerOfTwo(v uint64) uint {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return uint(v)
}

func getDepth(d uint64) uint8 {
	if d == 0 {
		return 0
	}
	if d == 1 {
		return 1
	}
	i := nextPowerOfTwo(d)
	return 64 - uint8(bits.LeadingZeros(i)) - 1
}

func (h *Hasher) doHash(dst []byte, a []byte, b []byte) []byte {
	h.hash.Write(a)
	h.hash.Write(b)
	h.hash.Sum(dst[:0])
	h.hash.Reset()
	return dst
}

func (h *Hasher) merkleizeImpl(dst []byte, input []byte, limit uint64) []byte {
	count := uint64(len(input) / 32)
	if limit == 0 {
		limit = count
	} else if count > limit {
		panic(fmt.Sprintf("BUG: count '%d' higher than limit '%d'", count, limit))
	}

	if limit == 0 {
		return append(dst, zeroBytes...)
	}
	if limit == 1 {
		if count == 1 {
			return append(dst, input[:32]...)
		}
		return append(dst, zeroBytes...)
	}

	depth := getDepth(count)
	h.merkleizeTmp = extendByteSlice(h.merkleizeTmp[:0], int(depth+2)*32)

	// reset tmp
	j := uint8(0)
	hh := h.merkleizeTmp[0:32]

	getTmp := func(i uint8) []byte {
		indx := (uint64(i) + 1) * 32
		return h.merkleizeTmp[indx : indx+32]
	}

	merge := func(i uint64, val []byte) {
		hh = append(hh[:0], val...)

		// merge back up from bottom to top, as far as we can
		for j = 0; ; j++ {
			// stop merging when we are in the left side of the next combi
			if i&(uint64(1)<<j) == 0 {
				// if we are at the count, we want to merge in zero-hashes for padding
				if i == count && j < depth {
					h.doHash(hh, hh, zeroHashes[j][:])
				} else {
					// store the merge result (may be no merge, i.e. bottom leaf node)
					copy(getTmp(j), hh)
					break
				}
			} else {
				// keep merging up if we are the right side
				h.doHash(hh, getTmp(j), hh)
			}
		}
	}

	// merge in leaf by leaf.
	for i := uint64(0); i < count; i++ {
		indx := i * 32
		merge(i, input[indx:indx+32])
	}

	// complement with 0 if empty, or if not the right power of 2
	if (uint64(1) << depth) != count {
		merge(count, zeroHashes[0][:])
	}

	// the next power of two may be smaller than the ultimate virtual size,
	// complement with zero-hashes at each depth.
	res := getTmp(depth)
	for j := depth; j < getDepth(limit); j++ {
		res = h.doHash(res, res, zeroHashes[j][:])[:32]
	}
	return append(dst, res...)
}

func merkleizeInput(input [][32]byte, limit uint64) [32]byte {
	if limit == 0 {
		return merkleizeVector(input, uint64(len(input)))
	} else {
		return merkleizeVector(input, limit)
	}
}

// MerkleizeVector uses our optimized routine to hash a list of 32-byte
// elements.
func merkleizeVector(elements [][32]byte, length uint64) [32]byte {
	dep := depth(length)
	// Return zerohash at depth
	if len(elements) == 0 {
		return zeroHashesRaw[dep]
	}
	for i := uint8(0); i < dep; i++ {
		layerLen := len(elements)
		oddNodeLength := layerLen%2 == 1
		if oddNodeLength {
			zerohash := zeroHashesRaw[i]
			elements = append(elements, zerohash)
		}
		outputLen := len(elements) / 2
		err := gohashtree.Hash(elements, elements)
		if err != nil {
			panic(err)
		}
		elements = elements[:outputLen]
	}
	return elements[0]
}

// Depth retrieves the appropriate depth for the provided trie size.
func depth(v uint64) (out uint8) {
	// bitmagic: binary search through a uint32, offset down by 1 to not round powers of 2 up.
	// Then adding 1 to it to not get the index of the first bit, but the length of the bits (depth of tree)
	// Zero is a special case, it has a 0 depth.
	// Example:
	//  (in out): (0 0), (1 0), (2 1), (3 2), (4 2), (5 3), (6 3), (7 3), (8 3), (9 4)
	if v <= 1 {
		return 0
	}
	v--
	if v&mask5 != 0 {
		v >>= bit5
		out |= bit5
	}
	if v&mask4 != 0 {
		v >>= bit4
		out |= bit4
	}
	if v&mask3 != 0 {
		v >>= bit3
		out |= bit3
	}
	if v&mask2 != 0 {
		v >>= bit2
		out |= bit2
	}
	if v&mask1 != 0 {
		v >>= bit1
		out |= bit1
	}
	if v&mask0 != 0 {
		out |= bit0
	}
	out++
	return
}
