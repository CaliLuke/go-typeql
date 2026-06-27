//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDriverHasOpenTransactionsTracksLifecycle(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := fmt.Sprintf("test_tx_operator_lifecycle_%d", time.Now().UnixNano())
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	open, err := conn.HasOpenTransactions(dbName)
	if err != nil {
		t.Fatalf("has open before tx: %v", err)
	}
	if open {
		t.Fatal("expected no open transactions before opening one")
	}

	tx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open write tx: %v", err)
	}
	open, err = conn.HasOpenTransactions(dbName)
	if err != nil {
		t.Fatalf("has open after tx: %v", err)
	}
	if !open {
		t.Fatal("expected open transaction to be tracked")
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback tx: %v", err)
	}
	open, err = conn.HasOpenTransactions(dbName)
	if err != nil {
		t.Fatalf("has open after rollback: %v", err)
	}
	if open {
		t.Fatal("expected rollback to unregister transaction")
	}

	schemaTx, err := conn.Transaction(dbName, Schema)
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	if _, err := schemaTx.Query("define attribute tx-operator-name, value string;"); err != nil {
		schemaTx.Close()
		t.Fatalf("define schema: %v", err)
	}
	if err := schemaTx.Commit(); err != nil {
		t.Fatalf("commit schema tx: %v", err)
	}
	open, err = conn.HasOpenTransactions(dbName)
	if err != nil {
		t.Fatalf("has open after commit: %v", err)
	}
	if open {
		t.Fatal("expected commit to unregister transaction")
	}

	readTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	readTx.Close()
	open, err = conn.HasOpenTransactions(dbName)
	if err != nil {
		t.Fatalf("has open after close: %v", err)
	}
	if open {
		t.Fatal("expected close to unregister transaction")
	}
}

func TestDriverCloseDatabaseTransactionsClosesOnlyMatchingDatabase(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbOne := fmt.Sprintf("test_tx_operator_close_one_%d", time.Now().UnixNano())
	dbTwo := fmt.Sprintf("test_tx_operator_close_two_%d", time.Now().UnixNano())
	dm := conn.Databases()
	for _, dbName := range []string{dbOne, dbTwo} {
		_ = dm.Delete(dbName)
		if err := dm.Create(dbName); err != nil {
			t.Fatalf("create db %s: %v", dbName, err)
		}
		defer dm.Delete(dbName)
	}

	txOne, err := conn.Transaction(dbOne, Read)
	if err != nil {
		t.Fatalf("open tx one: %v", err)
	}
	defer txOne.Close()
	txTwo, err := conn.Transaction(dbTwo, Read)
	if err != nil {
		t.Fatalf("open tx two: %v", err)
	}
	defer txTwo.Close()

	if err := conn.CloseDatabaseTransactions(context.Background(), dbOne); err != nil {
		t.Fatalf("close db one transactions: %v", err)
	}

	openOne, err := conn.HasOpenTransactions(dbOne)
	if err != nil {
		t.Fatalf("has open db one: %v", err)
	}
	if openOne {
		t.Fatal("expected db one transactions to be closed")
	}
	openTwo, err := conn.HasOpenTransactions(dbTwo)
	if err != nil {
		t.Fatalf("has open db two: %v", err)
	}
	if !openTwo {
		t.Fatal("expected db two transaction to remain open")
	}
	if txOne.IsOpen() {
		t.Fatal("expected db one transaction handle to be closed")
	}
	if !txTwo.IsOpen() {
		t.Fatal("expected db two transaction handle to remain open")
	}
}
