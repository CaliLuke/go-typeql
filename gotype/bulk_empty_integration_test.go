//go:build cgo && typedb && integration

package gotype_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/go-typeql/v2/driver"
	"github.com/CaliLuke/go-typeql/v2/gotype"
)

func TestIntegration_BulkEmptyStrings(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		for _, op := range []string{"insert", "put", "update", "update_with"} {
			t.Run(fmt.Sprintf("pooled_%v/%s", pooled, op), func(t *testing.T) {
				db := setupTestDBWith(t, func() { gotype.MustRegister[Company]() })
				if pooled {
					var err error
					db, err = gotype.NewDatabaseWithPool(gotype.PoolConfig{MaxSize: 1}, db.Name(), func() (gotype.Conn, error) {
						conn, err := driver.OpenWithTLS(dbAddress(), "admin", "password", false, "")
						if err != nil {
							return nil, err
						}
						return &driverAdapter{drv: conn}, nil
					})
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
				}
				ctx := context.Background()
				mgr := gotype.MustNewManager[Company](db)
				instances := []*Company{{Name: "a", Industry: ""}, {Name: "b", Industry: ""}}
				if op == "update" || op == "update_with" {
					for _, instance := range instances {
						instance.Industry = "before"
						if err := mgr.Insert(ctx, instance); err != nil {
							t.Fatal(err)
						}
						instance.Industry = ""
					}
				}
				var err error
				switch op {
				case "insert":
					err = mgr.InsertMany(ctx, instances)
				case "put":
					err = mgr.PutMany(ctx, instances)
				case "update":
					err = mgr.UpdateMany(ctx, instances)
				case "update_with":
					_, err = mgr.Query().UpdateWith(ctx, func(v *Company) { v.Industry = "" })
				}
				if err != nil {
					t.Fatal(err)
				}
				results, err := mgr.Get(ctx, nil)
				if err != nil || len(results) != 2 {
					t.Fatalf("read after bulk write: rows=%v err=%v", results, err)
				}
				for _, result := range results {
					if result.Industry != "" || result.GetIID() == "" {
						t.Fatalf("empty string did not round trip: %+v", result)
					}
				}
			})
		}
	}
}
