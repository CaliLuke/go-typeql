package gotype

import (
	"strings"
	"sync"
	"testing"
)

func TestProjectionCacheExactTextShapesAndMetadataEdits(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	for _, varName := range []string{"e", "other"} {
		for _, build := range []struct {
			name   string
			cached func() (string, error)
			fresh  func() (string, error)
		}{
			{"all", func() (string, error) { return buildFetchAll(info, varName) }, func() (string, error) { return compileFetchAll(info, varName) }},
			{"with type", func() (string, error) { return buildFetchAllWithType(info, varName) }, func() (string, error) { return compileFetchAllWithType(info, varName) }},
		} {
			t.Run(varName+"/"+build.name, func(t *testing.T) {
				for range 2 {
					got, err := build.cached()
					want, freshErr := build.fresh()
					if err != nil || freshErr != nil || got != want {
						t.Fatalf("cached=%q (%v), fresh=%q (%v)", got, err, want, freshErr)
					}
				}
			})
		}
	}
	info.Fields[0].Tag.Name = "renamed-attribute"
	got, err := buildFetchAll(info, "e")
	if err != nil || !strings.Contains(got, "renamed-attribute") {
		t.Fatalf("stale field projection %q: %v", got, err)
	}
	info.Fields[0].IsSlice = true
	got, err = buildFetchAll(info, "e")
	want, freshErr := compileFetchAll(info, "e")
	if err != nil || freshErr != nil || got != want {
		t.Fatalf("stale slice projection %q, want %q: %v/%v", got, want, err, freshErr)
	}
}

func TestProjectionCacheRoleRegistryReplacement(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testEmployment]())
	s := &relationStrategy{}
	firstAdd, firstFetch, err := s.BuildFetchWithRoles(info, "r")
	if err != nil {
		t.Fatal(err)
	}
	if add, fetch, err := buildFetchWithRoles(info, "r"); err != nil || add != firstAdd || fetch != firstFetch {
		t.Fatalf("role cache changed query text: %v", err)
	}
	ClearRegistry()
	_, missingFetch, err := s.BuildFetchWithRoles(info, "r")
	if err != nil || missingFetch == firstFetch {
		t.Fatalf("role cache survived registry clearing: %v", err)
	}
	MustRegister[testPerson]()
	_, replacedFetch, err := s.BuildFetchWithRoles(info, "r")
	if err != nil || replacedFetch == missingFetch {
		t.Fatalf("role cache ignored player replacement: %v", err)
	}
	player, _ := LookupType(typeOf[testPerson]())
	player.Fields[0].Tag.Name = "player-renamed"
	_, editedFetch, err := s.BuildFetchWithRoles(info, "r")
	if err != nil || !strings.Contains(editedFetch, "player-renamed") {
		t.Fatalf("role cache ignored player metadata edit: %v", err)
	}
	registerTestTypes(t)
}

func TestProjectionRoleSnapshotAcrossRegistryChange(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testEmployment]())
	players := projectionRolePlayers(info)
	signature := projectionSignature(info, players)
	_, before, err := buildFetchWithRolePlayers(info, "r", players)
	if err != nil {
		t.Fatal(err)
	}
	ClearRegistry()
	_, after, err := buildFetchWithRolePlayers(info, "r", players)
	if err != nil || after != before || signature != projectionSignature(info, players) {
		t.Fatalf("snapshot changed after registry clear: %v", err)
	}
	_, missing, err := (&relationStrategy{}).BuildFetchWithRoles(info, "r")
	if err != nil || missing == before {
		t.Fatalf("new lookup retained cleared player metadata: %v", err)
	}
	registerTestTypes(t)
}

func TestProjectionCacheConcurrentReads(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testCompany]())
	want, err := compileFetchAll(info, "e")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				got, err := buildFetchAll(info, "e")
				if err != nil || got != want {
					t.Errorf("concurrent projection=%q, err=%v", got, err)
				}
			}
		})
	}
	wg.Wait()
}

func TestProjectionCachePolymorphicSubtypeReplacement(t *testing.T) {
	ClearRegistry()
	MustRegister[zzzParentModel]()
	base, _ := LookupType(typeOf[zzzParentModel]())
	baseFetch, err := buildPolymorphicFetch(base, "e")
	if err != nil {
		t.Fatal(err)
	}
	MustRegister[aaaChildModel]()
	childFetch, err := buildPolymorphicFetch(base, "e")
	if err != nil || !strings.Contains(childFetch, "aaa-child-name") || childFetch == baseFetch {
		t.Fatalf("new subtype not reflected: %q, %v", childFetch, err)
	}
	if fresh, freshErr := compilePolymorphicFetch(base, "e", SubtypesOf(base.TypeName)); freshErr != nil || fresh != childFetch {
		t.Fatalf("cached polymorphic text differs from fresh: %v", freshErr)
	}
	child, _ := LookupType(typeOf[aaaChildModel]())
	child.Fields[0].Tag.Name = "subtype-renamed"
	editedFetch, err := buildPolymorphicFetch(base, "e")
	if err != nil || !strings.Contains(editedFetch, "subtype-renamed") {
		t.Fatalf("subtype edit not reflected: %v", err)
	}
	ClearRegistry()
	clearedFetch, err := buildPolymorphicFetch(base, "e")
	if err != nil || clearedFetch != baseFetch {
		t.Fatalf("registry clear retained subtype projection: %v", err)
	}
	MustRegister[aaaChildModel]()
	replacedFetch, err := buildPolymorphicFetch(base, "e")
	if err != nil || replacedFetch != childFetch {
		t.Fatalf("replacement subtype projection differs: %v", err)
	}
	registerTestTypes(t)
}
