//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_ForEachStreamsAndKeepsCallerTransaction(t *testing.T) {
	f := liveBenchSetup(t)
	registerProjectionLiveModels()
	ctx := context.Background()
	var kept []*liveBenchPerson
	if err := f.personMgr.ForEach(ctx, nil, func(person *liveBenchPerson) error {
		kept = append(kept, person)
		if len(kept) == 3 {
			return ErrStopIteration
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 || kept[0] == kept[1] || kept[0].GetIID() == "" {
		t.Fatalf("streamed models are not independently retained: %+v", kept)
	}
	scope, err := f.db.Begin(ReadTransaction)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	mgr := MustNewManagerWithTx[liveBenchPerson](scope)
	var names []string
	err = mgr.Query().OrderAsc("name").Offset(1).Limit(2).ForEach(ctx, func(person *liveBenchPerson) error {
		names = append(names, person.Name)
		return nil
	})
	if err != nil || len(names) != 2 || names[0] != "person-01" || names[1] != "person-02" {
		t.Fatalf("fluent streamed page=%v err=%v", names, err)
	}
	if !scope.Tx().IsOpen() {
		t.Fatal("caller transaction was closed")
	}
	var jobs int
	err = f.employMgr.ForEachWithRoles(ctx, nil, func(job *liveBenchEmployment) error {
		jobs++
		if job.Employee == nil || job.Employer == nil || job.Employee.Name == "" || job.Employer.Name == "" {
			t.Fatalf("role players were not hydrated: %+v", job)
		}
		return nil
	})
	if err != nil || jobs != liveBenchPersonCount {
		t.Fatalf("streamed relations=%d err=%v", jobs, err)
	}
}

func TestIntegration_ForEachCallbackError(t *testing.T) {
	f := liveBenchSetup(t)
	registerProjectionLiveModels()
	want := errors.New("consumer error")
	seen := 0
	err := f.personMgr.ForEach(context.Background(), nil, func(*liveBenchPerson) error {
		seen++
		return want
	})
	if !errors.Is(err, want) || seen != 1 {
		t.Fatalf("callback error=%v seen=%d", err, seen)
	}
}
