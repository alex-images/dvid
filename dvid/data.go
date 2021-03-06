/*
	This file contains the core DVID types that track data within repositories.
*/

package dvid

import (
	"encoding/binary"
	"fmt"

	"github.com/janelia-flyem/go/go-uuid/uuid"
)

// LocalID is a unique id for some data in a DVID instance.  This unique id is a much
// smaller representation than the actual data (e.g., a version UUID or data type url)
// and can be represented with fewer bytes in keys.
type LocalID uint16

// LocalID32 is a 32-bit unique id within this DVID instance.
type LocalID32 uint32

const (
	LocalIDSize   = 2
	LocalID32Size = 4

	MaxLocalID   = 0xFFFF
	MaxLocalID32 = 0xFFFFFFFF
)

// Bytes returns a sequence of bytes encoding this LocalID.  Binary representation
// will be big-endian to make integers lexigraphically
func (id LocalID) Bytes() []byte {
	buf := make([]byte, LocalIDSize, LocalIDSize)
	binary.BigEndian.PutUint16(buf, uint16(id))
	return buf
}

// LocalIDFromBytes returns a LocalID from the start of the slice and the number of bytes used.
// Note: No error checking is done to ensure byte slice has sufficient bytes for LocalID.
func LocalIDFromBytes(b []byte) (id LocalID, length int) {
	return LocalID(binary.BigEndian.Uint16(b)), LocalIDSize
}

// Bytes returns a sequence of bytes encoding this LocalID32.
func (id LocalID32) Bytes() []byte {
	buf := make([]byte, LocalID32Size, LocalID32Size)
	binary.BigEndian.PutUint32(buf, uint32(id))
	return buf
}

// LocalID32FromBytes returns a LocalID from the start of the slice and the number of bytes used.
// Note: No error checking is done to ensure byte slice has sufficient bytes for LocalID.
func LocalID32FromBytes(b []byte) (id LocalID32, length int) {
	return LocalID32(binary.BigEndian.Uint32(b)), LocalID32Size
}

// ---- Base identifiers of data within DVID -----

// UUID is a 32 character hexidecimal string ("" if invalid) that uniquely identifies
// nodes in a datastore's DAG.  We need universally unique identifiers to prevent collisions
// during creation of child nodes by distributed DVIDs:
// http://en.wikipedia.org/wiki/Universally_unique_identifier
type UUID string

// NewUUID returns a UUID
func NewUUID() UUID {
	u := uuid.NewUUID()
	if u == nil || len(u) != 16 {
		return UUID("")
	}
	return UUID(fmt.Sprintf("%032x", []byte(u)))
}

const NilUUID = UUID("")

// Note: TypeString and DataString are types to add static checks and prevent conflation
// of the two types of identifiers.

// TypeString is a string that is the name of a DVID data type.
type TypeString string

// URLString is a string representing a URL.
type URLString string

// DataString is a string that is the name of DVID data.
type DataString string

// InstanceID is a DVID server-specific identifier for data instances.  Each InstanceID
// is only used within one repo, so all key/values for a repo can be obtained by
// doing range queries on instances associated with a repo.  Valid InstanceIDs should
// be greater than 0.
type InstanceID LocalID32

// Bytes returns a sequence of bytes encoding this InstanceID.
func (id InstanceID) Bytes() []byte {
	buf := make([]byte, LocalID32Size, LocalID32Size)
	binary.BigEndian.PutUint32(buf, uint32(id))
	return buf
}

// InstanceIDFromBytes returns a LocalID from the start of the slice and the number of bytes used.
// Note: No error checking is done to ensure byte slice has sufficient bytes for InstanceID.
func InstanceIDFromBytes(b []byte) InstanceID {
	return InstanceID(binary.BigEndian.Uint32(b))
}

// RepoID is a DVID server-specific identifier for a particular Repo.  Valid RepoIDs
// should be greater than 0.
type RepoID LocalID32

// Bytes returns a sequence of bytes encoding this RepoID.  Binary representation is big-endian
// to preserve lexicographic order.
func (id RepoID) Bytes() []byte {
	buf := make([]byte, LocalID32Size, LocalID32Size)
	binary.BigEndian.PutUint32(buf, uint32(id))
	return buf
}

// RepoIDFromBytes returns a RepoID from the start of the slice and the number of bytes used.
// Note: No error checking is done to ensure byte slice has sufficient bytes for RepoID.
func RepoIDFromBytes(b []byte) RepoID {
	return RepoID(binary.BigEndian.Uint32(b))
}

// VersionID is a DVID server-specific identifier for a particular version or
// node of a repo's DAG.  Valid VersionIDs should be greater than 0.
type VersionID LocalID32

// Bytes returns a sequence of bytes encoding this VersionID.  Binary representation is big-endian
// to preserve lexicographic order.
func (id VersionID) Bytes() []byte {
	buf := make([]byte, LocalID32Size, LocalID32Size)
	binary.BigEndian.PutUint32(buf, uint32(id))
	return buf
}

// VersionIDFromBytes returns a VersionID from the start of the slice and the number of bytes used.
// Note: No error checking is done to ensure byte slice has sufficient bytes for VersionID.
func VersionIDFromBytes(b []byte) VersionID {
	return VersionID(binary.BigEndian.Uint32(b))
}

type InstanceMap map[InstanceID]InstanceID
type VersionMap map[VersionID]VersionID

const (
	MaxInstanceID = MaxLocalID32
	MaxRepoID     = MaxLocalID32
	MaxVersionID  = MaxLocalID32

	InstanceIDSize = 4
	RepoIDSize     = 4
	VersionIDSize  = 4
)

// Data is the minimal interface for datatype-specific data that is implemented
// in datatype packages.  It's required to say it's name, unique local instance ID,
// as well as whether it supports versioning.
type Data interface {
	DataName() DataString
	InstanceID() InstanceID

	SetInstanceID(InstanceID) // Necessary to support transmission of data to remote DVID.

	TypeName() TypeString
	TypeURL() URLString
	TypeVersion() string

	Versioned() bool
}

// Axis enumerates differnt types of axis (x, y, z, time, etc)
type Axis uint8

const (
	XAxis Axis = iota
	YAxis
	ZAxis
	TAxis
)

func (a Axis) String() string {
	switch a {
	case XAxis:
		return "X axis"
	case YAxis:
		return "Y axis"
	case ZAxis:
		return "Z axis"
	case TAxis:
		return "Time"
	default:
		return "Unknown"
	}
}
