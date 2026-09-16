package tqlgen

import (
	"fmt"
	"testing"
)

var benchmarkRolePlayer string

// roleLookupFixture puts the matching plays clauses last so each inherited
// lookup exercises the complete declaration scan, not parser or formatting.
func roleLookupFixture(types, roles, depth int) (*ParsedSchema, RelationSpec) {
	schema := &ParsedSchema{
		Entities:  make([]EntitySpec, types),
		Relations: make([]RelationSpec, depth),
	}
	for i := range schema.Entities {
		schema.Entities[i] = EntitySpec{
			Name:  fmt.Sprintf("entity-%d", i),
			Plays: []PlaysSpec{{Relation: "unrelated", Role: fmt.Sprintf("role-%d", i)}},
		}
	}
	for i := range schema.Relations {
		schema.Relations[i].Name = fmt.Sprintf("relation-%d", i)
		if i > 0 {
			schema.Relations[i].Parent = schema.Relations[i-1].Name
		}
	}
	root := &schema.Relations[0]
	leaf := &schema.Relations[depth-1]
	for i := range roles {
		role := fmt.Sprintf("target-%d", i)
		leaf.Relates = append(leaf.Relates, RelatesSpec{Role: role})
		schema.Entities[types-1].Plays = append(schema.Entities[types-1].Plays, PlaysSpec{Relation: root.Name, Role: role})
	}
	return schema, *leaf
}

// BenchmarkRolePlayerResolution includes index construction once per render
// and all role lookups, but excludes schema parsing, templates and gofmt.
func BenchmarkRolePlayerResolution(b *testing.B) {
	for _, types := range []int{16, 64, 256} {
		for _, roles := range []int{1, 4, 16, 32} {
			for _, depth := range []int{1, 4} {
				schema, leaf := roleLookupFixture(types, roles, depth)
				for _, indexed := range []bool{false, true} {
					name := "scan"
					if indexed {
						name = "index"
					}
					b.Run(fmt.Sprintf("types=%d/roles=%d/depth=%d/%s", types, roles, depth, name), func(b *testing.B) {
						r := newRenderer(schema, DefaultConfig())
						b.ReportAllocs()
						b.ReportMetric(float64(roles), "lookups/op")
						for range b.N {
							if indexed {
								// Include the production threshold dispatch on top of
								// construction, even when this case would not use it.
								_ = r.needsRolePlayerIndex()
								r.rolePlayers = buildRolePlayerIndex(schema)
							}
							for _, role := range leaf.Relates {
								benchmarkRolePlayer = r.findRolePlayer(leaf, role)
							}
						}
					})
				}
			}
		}
	}
}
