//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"testing"
)

func TestIntegration_ProjectedEntityAndRoleReads(t *testing.T) {
	f := liveBenchSetup(t)
	registerProjectionLiveModels()
	ctx := context.Background()
	people, err := f.personMgr.GetProjected(ctx, map[string]any{"name": "person-00"}, Projection{Fields: []string{"name"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 || people[0].IID != f.personIID || people[0].TypeName != "live-bench-person" {
		t.Fatalf("wrong projected person: %+v", people)
	}
	if people[0].Fields["name"].Value != "person-00" || len(people[0].Fields) != 1 {
		t.Fatalf("wrong projected fields: %+v", people[0].Fields)
	}

	jobs, err := f.employMgr.GetProjected(ctx, map[string]any{"since": int64(2000)}, Projection{
		Fields: []string{"since"},
		Roles:  map[string][]string{"employee": {"name"}, "employer": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Roles["employee"] == nil || jobs[0].Roles["employer"] == nil {
		t.Fatalf("wrong projected relation: %+v", jobs)
	}
	if jobs[0].Fields["since"].Value != float64(2000) || jobs[0].Roles["employee"].Fields["name"].Value != "person-00" {
		t.Fatalf("wrong relation fields: since=%#v (%T), employee=%#v (%T)", jobs[0].Fields["since"].Value, jobs[0].Fields["since"].Value, jobs[0].Roles["employee"].Fields["name"].Value, jobs[0].Roles["employee"].Fields["name"].Value)
	}
	if len(jobs[0].Roles["employer"].Fields) != 0 || jobs[0].Roles["employer"].IID == "" {
		t.Fatalf("employer did not retain only its identity: %+v", jobs[0].Roles["employer"])
	}
}

func TestIntegration_ProjectedFluentReadKeepsBoundTransaction(t *testing.T) {
	f := liveBenchSetup(t)
	registerProjectionLiveModels()
	ctx := context.Background()
	scope, err := f.db.Begin(ReadTransaction)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	mgr := MustNewManagerWithTx[liveBenchPerson](scope)
	people, err := mgr.Query().OrderAsc("name").Offset(1).Limit(2).ExecuteProjected(ctx, Projection{Fields: []string{"name"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 || people[0].Fields["name"].Value != "person-01" || people[1].Fields["name"].Value != "person-02" {
		t.Fatalf("wrong projected page: %+v", people)
	}
	if !scope.Tx().IsOpen() {
		t.Fatal("projected read closed caller-owned transaction")
	}
}

func registerProjectionLiveModels() {
	ClearRegistry()
	MustRegister[liveBenchPerson]()
	MustRegister[liveBenchCompany]()
	MustRegister[liveBenchEmployment]()
}
