/*
	This file supports keyspaces for voxel-related data types.  See doc for KeyType.
*/

package voxels

import (
	"encoding/binary"
	"fmt"

	"github.com/janelia-flyem/dvid/dvid"
	"github.com/janelia-flyem/dvid/storage"
)

// KeyType is the first byte of a type-specific index, allowing partitioning of the
// type-specific key space.
//
// Voxel block and label indexing is handled through a variety of key spaces that optimize
// throughput for access patterns required by our API.  It is essential that any
// data type has a consistent key space, including keys that might be generated by
// any derived data type that uses embedding.  For example, labels64 embeds the voxels
// data type and has a number of different index types to accelerate various lookups.
// These key spaces must not collide, so a common first byte is used where we explicitly
// demarcate the key spaces.
type KeyType byte

// For dcumentation purposes, consider the following key components:
//   a: original label
//   b: mapped label
//   s: spatial index (coordinate of a block)
//   v: # of voxels for a label
const (
	// KeyUnknown should never be used and is a check for corrupt or incorrectly set keys
	KeyUnknown KeyType = iota

	// KeyVoxelBlock have keys of form 's'
	KeyVoxelBlock

	// KeyForwardMap have keys of form 'a+b'
	// For superpixel->body maps, this key would be superpixel+body.
	KeyForwardMap

	// KeyInverseMap have keys of form 'b+a'
	KeyInverseMap

	// KeySpatialMap have keys of form 's+a+b'
	// They are useful for composing label maps for a spatial index.
	KeySpatialMap

	// KeyLabelSpatialMap have keys of form 'b+s' and have a sparse volume
	// encoding for its value. They are useful for returning all blocks
	// intersected by a label.
	KeyLabelSpatialMap

	// KeyLabelSizes have keys of form 'v+b'.
	// They allow rapid size range queries.
	KeyLabelSizes

	// KeyLabelSurface have keys of form 'b' and have the label's sparse volume
	// for its value.
	KeyLabelSurface
)

func (t KeyType) String() string {
	switch t {
	case KeyUnknown:
		return "Unknown Key Type"
	case KeyVoxelBlock:
		return "Voxel block"
	case KeyForwardMap:
		return "Forward Label Map"
	case KeyInverseMap:
		return "Inverse Label Map"
	case KeySpatialMap:
		return "Spatial Index to Labels Map"
	case KeyLabelSpatialMap:
		return "Forward Label to Spatial Index Map"
	case KeyLabelSizes:
		return "Forward Label sorted by volume"
	case KeyLabelSurface:
		return "Forward Label Surface"
	default:
		return "Unknown Key Type"
	}
}

// NewVoxelBlockIndexByCoord returns an index for a block coord in string format.
func NewVoxelBlockIndexByCoord(blockCoord string) []byte {
	sz := len(blockCoord)
	index := make([]byte, 1+sz)
	index[0] = byte(KeyVoxelBlock)
	copy(index[1:], blockCoord)
	return dvid.IndexBytes(index)
}

// NewVoxelBlockIndex returns an index for a voxel block.
// Index = s
func NewVoxelBlockIndex(blockIndex dvid.Index) []byte {
	coord := string(blockIndex.Bytes())
	return NewVoxelBlockIndexByCoord(coord)
}

// DecodeVoxelBlockKey returns a spatial index from a voxel block key.
// TODO: Extend this when necessary to allow any form of spatial indexing like CZYX.
func DecodeVoxelBlockKey(key []byte) (*dvid.IndexZYX, error) {
	var ctx storage.DataContext
	index, err := ctx.IndexFromKey(key)
	if err != nil {
		return nil, err
	}
	if index[0] != byte(KeyVoxelBlock) {
		return nil, fmt.Errorf("Expected KeyVoxelBlock index, got %d byte instead", index[0])
	}
	var zyx dvid.IndexZYX
	if err = zyx.IndexFromBytes(index[1:]); err != nil {
		return nil, fmt.Errorf("Cannot recover ZYX index from key %v: %s\n", key, err.Error())
	}
	return &zyx, nil
}

// NewForwardMapIndex returns an index for mapping a label into another label.
// Index = a+b
// For dcumentation purposes, consider the following key components:
//   a: original label
//   b: mapped label
//   s: spatial index (coordinate of a block)
//   v: # of voxels for a label
func NewForwardMapIndex(label []byte, mapping uint64) dvid.IndexBytes {
	index := make([]byte, 17)
	index[0] = byte(KeyForwardMap)
	copy(index[1:9], label)
	binary.BigEndian.PutUint64(index[9:17], mapping)
	return dvid.IndexBytes(index)
}

// NewInverseMapIndex returns an index for mapping a label into another label.
// Index = b+a
func NewInverseMapIndex(label []byte, mapping uint64) dvid.IndexBytes {
	index := make([]byte, 17)
	index[0] = byte(KeyInverseMap)
	binary.BigEndian.PutUint64(index[1:9], mapping)
	copy(index[9:17], label)
	return dvid.IndexBytes(index)
}

type SpatialMapIndex dvid.IndexBytes

// NewSpatialMapIndex returns an index optimizing access to label maps for a given
// spatial index. Index = s+a+b
func NewSpatialMapIndex(blockIndex dvid.Index, label []byte, mappedLabel uint64) SpatialMapIndex {
	indexBytes := blockIndex.Bytes()
	sz := len(indexBytes)
	index := make([]byte, 1+sz+8+8) // s + a + b
	index[0] = byte(KeySpatialMap)
	i := 1 + sz
	copy(index[1:i], indexBytes)
	if label != nil {
		copy(index[i:i+8], label)
	}
	binary.BigEndian.PutUint64(index[i+8:i+16], mappedLabel)
	return SpatialMapIndex(index)
}

func (index SpatialMapIndex) UpdateSpatialMapIndex(label []byte, mappedLabel uint64) {
	spatialSize := len(index) - 17
	i := 1 + spatialSize
	if label != nil {
		copy(index[i:i+8], label)
	}
	binary.BigEndian.PutUint64(index[i+8:i+16], mappedLabel)
}

// DecodeSpatialMapKey returns a label mapping from a spatial map key.
func DecodeSpatialMapKey(key []byte) (label []byte, mappedLabel uint64, err error) {
	var ctx storage.DataContext
	var index []byte
	index, err = ctx.IndexFromKey(key)
	if err != nil {
		return
	}
	if index[0] != byte(KeySpatialMap) {
		err = fmt.Errorf("Expected KeySpatialMap index, got %d byte instead", index[0])
		return
	}
	labelOffset := 1 + dvid.IndexZYXSize // index here = s + a + b
	label = index[labelOffset : labelOffset+8]
	mappedLabel = binary.BigEndian.Uint64(index[labelOffset+8 : labelOffset+16])
	return
}

// NewLabelSpatialMapIndex returns an identifier for storing a "label + spatial index", where
// the spatial index references a block that contains a voxel with the given label.
func NewLabelSpatialMapIndex(label uint64, blockBytes []byte) dvid.IndexBytes {
	sz := len(blockBytes)
	index := make([]byte, 1+8+sz)
	index[0] = byte(KeyLabelSpatialMap)
	binary.BigEndian.PutUint64(index[1:9], label)
	copy(index[9:], blockBytes)
	return dvid.IndexBytes(index)
}

// DecodeLabelSpatialMapKey returns a label and block index bytes from a LabelSpatialMap key.
// The block index bytes are returned because different block indices may be used (e.g., CZYX),
// and its up to caller to determine which one is used for this particular key.
func DecodeLabelSpatialMapKey(key []byte) (label uint64, blockBytes []byte, err error) {
	var ctx storage.DataContext
	var index []byte
	index, err = ctx.IndexFromKey(key)
	if err != nil {
		return
	}
	if index[0] != byte(KeyLabelSpatialMap) {
		err = fmt.Errorf("Expected KeyLabelSpatialMap index, got %d byte instead", index[0])
		return
	}
	label = binary.BigEndian.Uint64(index[1:9])
	blockBytes = index[9:]
	return
}

// NewLabelSizesIndex returns an identifier for storing a "size + mapped label".
func NewLabelSizesIndex(size, label uint64) dvid.IndexBytes {
	index := make([]byte, 17)
	index[0] = byte(KeyLabelSizes)
	binary.BigEndian.PutUint64(index[1:9], size)
	binary.BigEndian.PutUint64(index[9:17], label)
	return dvid.IndexBytes(index)
}

func LabelFromLabelSizesKey(key []byte) (uint64, error) {
	ctx := &storage.DataContext{}
	indexBytes, err := ctx.IndexFromKey(key)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(indexBytes[9:17]), nil
}

// NewLabelSurfaceIndex returns an identifier for a given label's surface.
func NewLabelSurfaceIndex(label uint64) dvid.IndexBytes {
	index := make([]byte, 1+8)
	index[0] = byte(KeyLabelSurface)
	binary.BigEndian.PutUint64(index[1:9], label)
	return dvid.IndexBytes(index)
}
