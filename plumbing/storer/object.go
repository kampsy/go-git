package storer

import (
	"errors"
	"io"

	"gopkg.in/src-d/go-git.v4/plumbing"
)

var (
	//ErrStop is used to stop a ForEach function in an Iter
	ErrStop = errors.New("stop iter")
)

// ObjectStorer generic storage of objects
type ObjectStorer interface {
	// NewObject returns a new plumbing.Object, the real type of the object can
	// be a custom implementation or the defaul one, plumbing.MemoryObject
	NewObject() plumbing.Object
	// SetObject save an object into the storage, the object shuld be create
	// with the NewObject, method, and file if the type is not supported.
	SetObject(plumbing.Object) (plumbing.Hash, error)
	// Object get an object by hash with the given plumbing.ObjectType.
	// Implementors should return (nil, plumbing.ErrObjectNotFound) if an object
	// doesn't exist with both the given hash and object type.
	//
	// Valid plumbing.ObjectType values are CommitObject, BlobObject, TagObject,
	// TreeObject and AnyObject. If plumbing.AnyObject is given, the object must
	// be looked up regardless of its type.
	Object(plumbing.ObjectType, plumbing.Hash) (plumbing.Object, error)
	// IterObjects returns a custom ObjectIter over all the object on the
	// storage.
	//
	// Valid plumbing.ObjectType values are CommitObject, BlobObject, TagObject,
	IterObjects(plumbing.ObjectType) (ObjectIter, error)
}

// Transactioner is a optional method for ObjectStorer, it enable transaction
// base write and read operations in the storage
type Transactioner interface {
	// Begin starts a transaction.
	Begin() Transaction
}

// PackfileWriter is a optional method for ObjectStorer, it enable direct write
// of packfile to the storage
type PackfileWriter interface {
	// PackfileWriter retuns a writer for writing a packfile to the storage
	//
	// If the Storer not implements PackfileWriter the objects should be written
	// using the Set method.
	PackfileWriter() (io.WriteCloser, error)
}

// ObjectIter is a generic closable interface for iterating over objects.
type ObjectIter interface {
	Next() (plumbing.Object, error)
	ForEach(func(plumbing.Object) error) error
	Close()
}

// Transaction is an in-progress storage transaction. A transaction must end
// with a call to Commit or Rollback.
type Transaction interface {
	SetObject(plumbing.Object) (plumbing.Hash, error)
	Object(plumbing.ObjectType, plumbing.Hash) (plumbing.Object, error)
	Commit() error
	Rollback() error
}

// ObjectLookupIter implements ObjectIter. It iterates over a series of object
// hashes and yields their associated objects by retrieving each one from
// object storage. The retrievals are lazy and only occur when the iterator
// moves forward with a call to Next().
//
// The ObjectLookupIter must be closed with a call to Close() when it is no
// longer needed.
type ObjectLookupIter struct {
	storage ObjectStorer
	series  []plumbing.Hash
	t       plumbing.ObjectType
	pos     int
}

// NewObjectLookupIter returns an object iterator given an object storage and
// a slice of object hashes.
func NewObjectLookupIter(
	storage ObjectStorer, t plumbing.ObjectType, series []plumbing.Hash) *ObjectLookupIter {
	return &ObjectLookupIter{
		storage: storage,
		series:  series,
		t:       t,
	}
}

// Next returns the next object from the iterator. If the iterator has reached
// the end it will return io.EOF as an error. If the object can't be found in
// the object storage, it will return plumbing.ErrObjectNotFound as an error.
// If the object is retreieved successfully error will be nil.
func (iter *ObjectLookupIter) Next() (plumbing.Object, error) {
	if iter.pos >= len(iter.series) {
		return nil, io.EOF
	}

	hash := iter.series[iter.pos]
	obj, err := iter.storage.Object(iter.t, hash)
	if err == nil {
		iter.pos++
	}

	return obj, err
}

// ForEach call the cb function for each object contained on this iter until
// an error happends or the end of the iter is reached. If ErrStop is sent
// the iteration is stop but no error is returned. The iterator is closed.
func (iter *ObjectLookupIter) ForEach(cb func(plumbing.Object) error) error {
	return ForEachIterator(iter, cb)
}

// Close releases any resources used by the iterator.
func (iter *ObjectLookupIter) Close() {
	iter.pos = len(iter.series)
}

// ObjectSliceIter implements ObjectIter. It iterates over a series of objects
// stored in a slice and yields each one in turn when Next() is called.
//
// The ObjectSliceIter must be closed with a call to Close() when it is no
// longer needed.
type ObjectSliceIter struct {
	series []plumbing.Object
	pos    int
}

// NewObjectSliceIter returns an object iterator for the given slice of objects.
func NewObjectSliceIter(series []plumbing.Object) *ObjectSliceIter {
	return &ObjectSliceIter{
		series: series,
	}
}

// Next returns the next object from the iterator. If the iterator has reached
// the end it will return io.EOF as an error. If the object is retreieved
// successfully error will be nil.
func (iter *ObjectSliceIter) Next() (plumbing.Object, error) {
	if len(iter.series) == 0 {
		return nil, io.EOF
	}

	obj := iter.series[0]
	iter.series = iter.series[1:]

	return obj, nil
}

// ForEach call the cb function for each object contained on this iter until
// an error happends or the end of the iter is reached. If ErrStop is sent
// the iteration is stop but no error is returned. The iterator is closed.
func (iter *ObjectSliceIter) ForEach(cb func(plumbing.Object) error) error {
	return ForEachIterator(iter, cb)
}

// Close releases any resources used by the iterator.
func (iter *ObjectSliceIter) Close() {
	iter.series = []plumbing.Object{}
}

// MultiObjectIter implements ObjectIter. It iterates over several ObjectIter,
//
// The MultiObjectIter must be closed with a call to Close() when it is no
// longer needed.
type MultiObjectIter struct {
	iters []ObjectIter
	pos   int
}

// NewMultiObjectIter returns an object iterator for the given slice of objects.
func NewMultiObjectIter(iters []ObjectIter) ObjectIter {
	return &MultiObjectIter{iters: iters}
}

// Next returns the next object from the iterator, if one iterator reach io.EOF
// is removed and the next one is used.
func (iter *MultiObjectIter) Next() (plumbing.Object, error) {
	if len(iter.iters) == 0 {
		return nil, io.EOF
	}

	obj, err := iter.iters[0].Next()
	if err == io.EOF {
		iter.iters[0].Close()
		iter.iters = iter.iters[1:]
		return iter.Next()
	}

	return obj, err
}

// ForEach call the cb function for each object contained on this iter until
// an error happends or the end of the iter is reached. If ErrStop is sent
// the iteration is stop but no error is returned. The iterator is closed.
func (iter *MultiObjectIter) ForEach(cb func(plumbing.Object) error) error {
	return ForEachIterator(iter, cb)
}

// Close releases any resources used by the iterator.
func (iter *MultiObjectIter) Close() {
	for _, i := range iter.iters {
		i.Close()
	}
}

type bareIterator interface {
	Next() (plumbing.Object, error)
	Close()
}

// ForEachIterator is a helper function to build iterators without need to
// rewrite the same ForEach function each time.
func ForEachIterator(iter bareIterator, cb func(plumbing.Object) error) error {
	defer iter.Close()
	for {
		obj, err := iter.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}

			return err
		}

		if err := cb(obj); err != nil {
			if err == ErrStop {
				return nil
			}

			return err
		}
	}
}
