package ast

import (
	"testing"
	"time"
)

func TestCompiler_MatchClause(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node QueryNode
		want string
	}{
		{
			name: "simple entity match",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{Variable: "$p", TypeName: "person"},
				},
			},
			want: "match\n$p isa person;",
		},
		{
			name: "entity with has constraint",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{
						Variable: "$p",
						TypeName: "person",
						Constraints: []Constraint{
							HasConstraint{AttrName: "name", Value: LiteralValue{Val: "Alice", ValueType: "string"}},
						},
					},
				},
			},
			want: `match
$p isa person, has name "Alice";`,
		},
		{
			name: "entity with strict isa",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{Variable: "$p", TypeName: "person", IsStrict: true},
				},
			},
			want: "match\n$p isa! person;",
		},
		{
			name: "entity with iid constraint",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{
						Variable: "$p",
						TypeName: "person",
						Constraints: []Constraint{
							IidConstraint{IID: "0x12345"},
						},
					},
				},
			},
			want: "match\n$p isa person, iid 0x12345;",
		},
		{
			name: "relation pattern with role players",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "employment",
						RolePlayers: []RolePlayer{
							{Role: "employee", PlayerVar: "$p"},
							{Role: "employer", PlayerVar: "$c"},
						},
					},
				},
			},
			want: "match\n$r isa employment (employee: $p, employer: $c);",
		},
		{
			name: "multiple patterns",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{Variable: "$p", TypeName: "person"},
					HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
				},
			},
			want: "match\n$p isa person;\n$p has name $n;",
		},
		{
			name: "subtype pattern",
			node: MatchClause{
				Patterns: []Pattern{
					SubTypePattern{Variable: "$t", ParentType: "entity"},
				},
			},
			want: "match\n$t sub entity;",
		},
		{
			name: "value comparison pattern",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{Variable: "$p", TypeName: "person"},
					HasPattern{ThingVar: "$p", AttrType: "age", AttrVar: "$a"},
					ValueComparisonPattern{Var: "$a", Operator: ">", Value: LiteralValue{Val: int64(30), ValueType: "long"}},
				},
			},
			want: "match\n$p isa person;\n$p has age $a;\n$a > 30;",
		},
		{
			name: "not pattern",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{Variable: "$p", TypeName: "person"},
					NotPattern{
						Patterns: []Pattern{
							HasPattern{ThingVar: "$p", AttrType: "email", AttrVar: "$e"},
						},
					},
				},
			},
			want: "match\n$p isa person;\nnot { $p has email $e; };",
		},
		{
			name: "or pattern",
			node: MatchClause{
				Patterns: []Pattern{
					EntityPattern{Variable: "$p", TypeName: "person"},
					OrPattern{
						Alternatives: [][]Pattern{
							{HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"}},
							{HasPattern{ThingVar: "$p", AttrType: "email", AttrVar: "$e"}},
						},
					},
				},
			},
			want: "match\n$p isa person;\n{ $p has name $n; } or { $p has email $e; };",
		},
		{
			name: "iid pattern",
			node: MatchClause{
				Patterns: []Pattern{
					IidPattern{Variable: "$x", IID: "0xabc"},
				},
			},
			want: "match\n$x iid 0xabc;",
		},
		{
			name: "attribute pattern without value",
			node: MatchClause{
				Patterns: []Pattern{
					AttributePattern{Variable: "$a", TypeName: "age"},
				},
			},
			want: "match\n$a isa age;",
		},
		{
			name: "attribute pattern with value",
			node: MatchClause{
				Patterns: []Pattern{
					AttributePattern{
						Variable: "$a",
						TypeName: "age",
						Value:    LiteralValue{Val: int64(30), ValueType: "long"},
					},
				},
			},
			want: "match\n$a isa age; $a 30;",
		},
		{
			name: "raw pattern",
			node: MatchClause{
				Patterns: []Pattern{
					RawPattern{Content: "$x isa custom-thing"},
				},
			},
			want: "match\n$x isa custom-thing;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestCompiler_InsertClause(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node QueryNode
		want string
	}{
		{
			name: "insert entity",
			node: InsertClause{
				Statements: []Statement{
					IsaStatement{Variable: "$e", TypeName: "person"},
					HasStatement{SubjectVar: "$e", AttrName: "name", Value: LiteralValue{Val: "Alice", ValueType: "string"}},
					HasStatement{SubjectVar: "$e", AttrName: "age", Value: LiteralValue{Val: int64(30), ValueType: "long"}},
				},
			},
			want: `insert
$e isa person;
$e has name "Alice";
$e has age 30;`,
		},
		{
			name: "insert relation with variable",
			node: InsertClause{
				Statements: []Statement{
					RelationStatement{
						Variable:        "$r",
						TypeName:        "employment",
						IncludeVariable: true,
						RolePlayers: []RolePlayer{
							{Role: "employee", PlayerVar: "$p"},
							{Role: "employer", PlayerVar: "$c"},
						},
					},
				},
			},
			want: "insert\n$r isa employment, links (employee: $p, employer: $c);",
		},
		{
			name: "insert relation without variable (3.x style)",
			node: InsertClause{
				Statements: []Statement{
					RelationStatement{
						Variable:        "$r",
						TypeName:        "employment",
						IncludeVariable: false,
						RolePlayers: []RolePlayer{
							{Role: "employee", PlayerVar: "$p"},
							{Role: "employer", PlayerVar: "$c"},
						},
					},
				},
			},
			want: "insert\n(employee: $p, employer: $c) isa employment;",
		},
		{
			name: "insert relation with inline attributes",
			node: InsertClause{
				Statements: []Statement{
					RelationStatement{
						Variable:        "$r",
						TypeName:        "employment",
						IncludeVariable: false,
						RolePlayers: []RolePlayer{
							{Role: "employee", PlayerVar: "$p"},
							{Role: "employer", PlayerVar: "$c"},
						},
						Attributes: []HasStatement{
							{SubjectVar: "$r", AttrName: "start-date", Value: LiteralValue{Val: "2024-01-01", ValueType: "string"}},
						},
					},
				},
			},
			want: `insert
(employee: $p, employer: $c) isa employment, has start-date "2024-01-01";`,
		},
		{
			name: "raw statement",
			node: InsertClause{
				Statements: []Statement{
					RawStatement{Content: "$x isa something"},
				},
			},
			want: "insert\n$x isa something;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestCompiler_DeleteClause(t *testing.T) {
	c := &Compiler{}
	got, err := c.Compile(DeleteClause{
		Statements: []Statement{
			DeleteThingStatement{Variable: "$p"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "delete\n$p;"
	if got != want {
		t.Errorf("got: %q, want: %q", got, want)
	}
}

func TestCompiler_UpdateClause(t *testing.T) {
	c := &Compiler{}
	got, err := c.Compile(UpdateClause{
		Statements: []Statement{
			HasStatement{SubjectVar: "$p", AttrName: "age", Value: LiteralValue{Val: int64(31), ValueType: "long"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "update\n$p has age 31;"
	if got != want {
		t.Errorf("got: %q, want: %q", got, want)
	}
}

func TestCompiler_FetchClause(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node FetchClause
		want string
	}{
		{
			name: "fetch attributes",
			node: FetchClause{
				Items: []any{
					FetchAttribute{Key: "name", Var: "$p", AttrName: "name"},
					FetchAttribute{Key: "age", Var: "$p", AttrName: "age"},
				},
			},
			want: `fetch {
  "name": $p.name,
  "age": $p.age
};`,
		},
		{
			name: "fetch variable",
			node: FetchClause{
				Items: []any{
					FetchVariable{Key: "person", Var: "$p"},
				},
			},
			want: `fetch {
  "person": $p
};`,
		},
		{
			name: "fetch attribute list",
			node: FetchClause{
				Items: []any{
					FetchAttributeList{Key: "tags", Var: "$p", AttrName: "tag"},
				},
			},
			want: `fetch {
  "tags": [$p.tag]
};`,
		},
		{
			name: "fetch function",
			node: FetchClause{
				Items: []any{
					FetchFunction{Key: "_iid", FuncName: "iid", Var: "$p"},
				},
			},
			want: `fetch {
  "_iid": iid($p)
};`,
		},
		{
			name: "fetch wildcard",
			node: FetchClause{
				Items: []any{
					FetchWildcard{Key: "attrs", Var: "$p"},
				},
			},
			want: `fetch {
  "attrs": $p.*
};`,
		},
		{
			name: "fetch nested wildcard",
			node: FetchClause{
				Items: []any{
					FetchNestedWildcard{Key: "nested", Var: "$p"},
				},
			},
			want: `fetch {
  "nested": { $p.* }
};`,
		},
		{
			name: "fetch with raw string",
			node: FetchClause{
				Items: []any{
					"\"custom\": $x.custom",
				},
			},
			want: `fetch {
  "custom": $x.custom
};`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestCompiler_ReduceClause(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node ReduceClause
		want string
	}{
		{
			name: "simple count",
			node: ReduceClause{
				Assignments: []ReduceAssignment{
					{Variable: "$count", Expression: "count($p)"},
				},
			},
			want: "reduce $count = count($p);",
		},
		{
			name: "reduce with groupby",
			node: ReduceClause{
				Assignments: []ReduceAssignment{
					{Variable: "$count", Expression: "count($p)"},
				},
				GroupBy: "$city",
			},
			want: "reduce $count = count($p) groupby $city;",
		},
		{
			name: "reduce with function call value",
			node: ReduceClause{
				Assignments: []ReduceAssignment{
					{Variable: "$total", Expression: FunctionCallValue{Function: "sum", Args: []any{"$salary"}}},
				},
			},
			want: "reduce $total = sum($salary);",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got: %q, want: %q", got, tt.want)
			}
		})
	}
}

func TestCompiler_MatchLetClause(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node MatchLetClause
		want string
	}{
		{
			name: "let assignment",
			node: MatchLetClause{
				Assignments: []LetAssignment{
					{Variables: []string{"$x"}, Expression: "iid($p)"},
				},
			},
			want: "match\nlet $x = iid($p);",
		},
		{
			name: "let stream assignment",
			node: MatchLetClause{
				Assignments: []LetAssignment{
					{Variables: []string{"$x"}, Expression: "iid($p)", IsStream: true},
				},
			},
			want: "match\nlet $x in iid($p);",
		},
		{
			name: "let with function call value",
			node: MatchLetClause{
				Assignments: []LetAssignment{
					{Variables: []string{"$x"}, Expression: FunctionCallValue{Function: "iid", Args: []any{"$p"}}},
				},
			},
			want: "match\nlet $x = iid($p);",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got: %q, want: %q", got, tt.want)
			}
		})
	}
}

func TestCompiler_Values(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node Value
		want string
	}{
		{
			name: "string literal",
			node: LiteralValue{Val: "hello", ValueType: "string"},
			want: `"hello"`,
		},
		{
			name: "string with escapes",
			node: LiteralValue{Val: "say \"hi\"\nnewline", ValueType: "string"},
			want: `"say \"hi\"\nnewline"`,
		},
		{
			name: "long literal",
			node: LiteralValue{Val: int64(42), ValueType: "long"},
			want: "42",
		},
		{
			name: "double literal",
			node: LiteralValue{Val: 3.14, ValueType: "double"},
			want: "3.14",
		},
		{
			name: "boolean true",
			node: LiteralValue{Val: true, ValueType: "boolean"},
			want: "true",
		},
		{
			name: "boolean false",
			node: LiteralValue{Val: false, ValueType: "boolean"},
			want: "false",
		},
		{
			name: "datetime",
			node: LiteralValue{Val: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC), ValueType: "datetime"},
			want: "2024-01-15T10:30:00",
		},
		{
			name: "date",
			node: LiteralValue{Val: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), ValueType: "date"},
			want: "2024-01-15",
		},
		{
			name: "arithmetic value",
			node: ArithmeticValue{Left: "$x", Operator: "+", Right: LiteralValue{Val: int64(1), ValueType: "long"}},
			want: "($x + 1)",
		},
		{
			name: "function call value",
			node: FunctionCallValue{Function: "iid", Args: []any{"$x"}},
			want: "iid($x)",
		},
		{
			name: "function call with literal arg",
			node: FunctionCallValue{Function: "abs", Args: []any{LiteralValue{Val: int64(-5), ValueType: "long"}}},
			want: "abs(-5)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got: %q, want: %q", got, tt.want)
			}
		})
	}
}

func TestCompiler_CompileBatch(t *testing.T) {
	c := &Compiler{}
	nodes := []QueryNode{
		MatchClause{
			Patterns: []Pattern{
				EntityPattern{Variable: "$p", TypeName: "person"},
			},
		},
		FetchClause{
			Items: []any{
				FetchAttribute{Key: "name", Var: "$p", AttrName: "name"},
			},
		},
	}
	got, err := c.CompileBatch(nodes, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "match\n$p isa person;\nfetch {\n  \"name\": $p.name\n};"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCompiler_RelationConstraints(t *testing.T) {
	c := &Compiler{}
	got, err := c.Compile(MatchClause{
		Patterns: []Pattern{
			RelationPattern{
				Variable: "$r",
				TypeName: "employment",
				RolePlayers: []RolePlayer{
					{Role: "employee", PlayerVar: "$p"},
				},
				Constraints: []Constraint{
					HasConstraint{AttrName: "salary", Value: LiteralValue{Val: int64(50000), ValueType: "long"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "match\n$r isa employment (employee: $p), has salary 50000;"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCompiler_TypelessRelation(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node MatchClause
		want string
	}{
		{
			name: "typeless relation with role players",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "",
						RolePlayers: []RolePlayer{
							{Role: "links", PlayerVar: "$a"},
							{Role: "links", PlayerVar: "$b"},
						},
					},
				},
			},
			want: "match\n$r, links ($a), links ($b);",
		},
		{
			name: "typeless relation without role players",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "",
					},
				},
			},
			want: "match\n$r;",
		},
		{
			name: "typed relation with role players",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "friendship",
						RolePlayers: []RolePlayer{
							{Role: "friend", PlayerVar: "$a"},
							{Role: "friend", PlayerVar: "$b"},
						},
					},
				},
			},
			want: "match\n$r isa friendship (friend: $a, friend: $b);",
		},
		{
			name: "typed relation without role players",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "employment",
					},
				},
			},
			want: "match\n$r isa employment;",
		},
		{
			name: "typed relation with links role players",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "task-depends-on",
						RolePlayers: []RolePlayer{
							{Role: "links", PlayerVar: "$a"},
							{Role: "links", PlayerVar: "$b"},
						},
					},
				},
			},
			want: "match\n$r isa task-depends-on, links ($a), links ($b);",
		},
		{
			name: "relation with mixed links and regular roles",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "some-relation",
						RolePlayers: []RolePlayer{
							{Role: "links", PlayerVar: "$a"},
							{Role: "links", PlayerVar: "$b"},
							{Role: "player", PlayerVar: "$c"},
						},
					},
				},
			},
			want: "match\n$r isa some-relation (player: $c), links ($a), links ($b);",
		},
		{
			name: "typeless relation with constraints",
			node: MatchClause{
				Patterns: []Pattern{
					RelationPattern{
						Variable: "$r",
						TypeName: "",
						RolePlayers: []RolePlayer{
							{Role: "links", PlayerVar: "$a"},
						},
						Constraints: []Constraint{
							IidConstraint{IID: "0xabc"},
						},
					},
				},
			},
			want: "match\n$r, links ($a), iid 0xabc;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}
