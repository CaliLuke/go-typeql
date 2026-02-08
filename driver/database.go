//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"
import (
	"encoding/json"
	"unsafe"
)

// DatabaseManager provides methods for administrative operations on TypeDB databases,
// such as creating, deleting, and listing databases.
type DatabaseManager struct {
	driver *Driver
}

// All returns the names of all databases currently existing on the server.
func (dm *DatabaseManager) All() ([]string, error) {
	dm.driver.mu.Lock()
	defer dm.driver.mu.Unlock()

	if dm.driver.ptr == nil {
		return nil, ErrNotConnected
	}

	var dbErr *C.char
	cJSON := C.typedb_databases_all(dm.driver.ptr, &dbErr)
	if cJSON == nil {
		if err := getError(dbErr); err != nil {
			return nil, err
		}
		return nil, nil
	}
	defer C.typedb_free_string(cJSON)

	jsonStr := C.GoString(cJSON)
	var names []string
	if err := json.Unmarshal([]byte(jsonStr), &names); err != nil {
		return nil, &DriverError{Message: "failed to parse database list: " + err.Error()}
	}
	return names, nil
}

// Create creates a new database with the given name on the server.
func (dm *DatabaseManager) Create(name string) error {
	dm.driver.mu.Lock()
	defer dm.driver.mu.Unlock()

	if dm.driver.ptr == nil {
		return ErrNotConnected
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var createErr *C.char
	C.typedb_databases_create(dm.driver.ptr, cName, &createErr)
	return getError(createErr)
}

// Contains returns true if a database with the specified name exists on the server.
func (dm *DatabaseManager) Contains(name string) (bool, error) {
	dm.driver.mu.Lock()
	defer dm.driver.mu.Unlock()

	if dm.driver.ptr == nil {
		return false, ErrNotConnected
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var containsErr *C.char
	result := bool(C.typedb_databases_contains(dm.driver.ptr, cName, &containsErr))
	if err := getError(containsErr); err != nil {
		return false, err
	}
	return result, nil
}

// Schema returns the full schema of the specified database as a TypeQL 'define' query string.
func (dm *DatabaseManager) Schema(name string) (string, error) {
	dm.driver.mu.Lock()
	defer dm.driver.mu.Unlock()

	if dm.driver.ptr == nil {
		return "", ErrNotConnected
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var schemaErr *C.char
	cSchema := C.typedb_database_schema(dm.driver.ptr, cName, &schemaErr)
	if cSchema == nil {
		if err := getError(schemaErr); err != nil {
			return "", err
		}
		return "", nil
	}
	defer C.typedb_free_string(cSchema)

	return C.GoString(cSchema), nil
}

// Delete permanently removes the database with the specified name from the server.
func (dm *DatabaseManager) Delete(name string) error {
	dm.driver.mu.Lock()
	defer dm.driver.mu.Unlock()

	if dm.driver.ptr == nil {
		return ErrNotConnected
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var deleteErr *C.char
	C.typedb_database_delete(dm.driver.ptr, cName, &deleteErr)
	return getError(deleteErr)
}
