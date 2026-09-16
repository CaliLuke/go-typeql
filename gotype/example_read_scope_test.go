package gotype

import (
	"context"
	"fmt"
)

func ExampleDatabase_BeginContext() {
	ClearRegistry()
	MustRegister[testPerson]()
	MustRegister[testCompany]()
	readTx := &mockTx{responses: [][]map[string]any{
		{{"_iid": "0x01", "name": "Alice", "email": "alice@example.com"}},
		{{"_iid": "0x02", "name": "Acme", "industry": "Tools"}},
	}}
	db := NewDatabase(&mockConn{txs: []*mockTx{readTx}}, "example")
	ctx := context.Background()
	scope, err := db.BeginContext(ctx, ReadTransaction)
	if err != nil {
		panic(err)
	}
	defer scope.Close()

	persons := MustNewManagerWithTx[testPerson](scope)
	companies := MustNewManagerWithTx[testCompany](scope)
	alice, err := persons.GetByIID(ctx, "0x01")
	if err != nil {
		panic(err)
	}
	acme, err := companies.GetByIID(ctx, "0x02")
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s at %s\n", alice.Name, acme.Name)
	// Output: Alice at Acme
}
