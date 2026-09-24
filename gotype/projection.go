package gotype

import (
	"context"
	"fmt"
	"strings"
)

// Projection selects TypeDB attribute names from the root model and its role
// players. An empty Fields slice selects no attributes. An absent role map
// selects no role players. The IID and concrete type are always selected.
type Projection struct {
	Fields []string
	Roles  map[string][]string
}

// ProjectedField distinguishes a requested missing scalar from an omitted
// field. Value contains a driver-decoded TypeDB value when Present is true.
// A requested multi-valued attribute is present as a slice, even when empty.
type ProjectedField struct {
	Present bool
	Value   any
}

// ProjectedResult is read-only projection data, not a model instance.
// Fields contains only requested attributes. Roles contains only requested
// role names. Selecting a role requires that link, so missing links exclude
// the relation from the query result.
// IID and TypeName identify the concrete TypeDB instance.
type ProjectedResult struct {
	IID      string
	TypeName string
	Fields   map[string]ProjectedField
	Roles    map[string]*ProjectedResult
}

type projectionPlan struct {
	rootFields []FieldInfo
	roles      []projectionRole
}

type projectionRole struct {
	name   string
	fields []FieldInfo
}

func planProjection(info *ModelInfo, spec Projection) (projectionPlan, error) {
	fields, err := selectProjectionFields(info, spec.Fields)
	if err != nil {
		return projectionPlan{}, err
	}
	plan := projectionPlan{rootFields: fields}
	for _, role := range info.Roles {
		selected, ok := spec.Roles[role.RoleName]
		if !ok {
			continue
		}
		player, found := Lookup(role.PlayerTypeName)
		if !found {
			if len(selected) != 0 {
				return projectionPlan{}, fmt.Errorf("projection role %q: player type %q is not registered", role.RoleName, role.PlayerTypeName)
			}
			plan.roles = append(plan.roles, projectionRole{name: role.RoleName})
			continue
		}
		roleFields, err := selectProjectionFields(player, selected)
		if err != nil {
			return projectionPlan{}, fmt.Errorf("projection role %q: %w", role.RoleName, err)
		}
		plan.roles = append(plan.roles, projectionRole{name: role.RoleName, fields: roleFields})
	}
	for name := range spec.Roles {
		found := false
		for _, role := range info.Roles {
			if role.RoleName == name {
				found = true
				break
			}
		}
		if !found {
			return projectionPlan{}, fmt.Errorf("projection: unknown role %q on %s", name, info.TypeName)
		}
	}
	return plan, nil
}

func selectProjectionFields(info *ModelInfo, names []string) ([]FieldInfo, error) {
	fields := make([]FieldInfo, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		field, ok := info.FieldByAttrName(name)
		if !ok {
			return nil, fmt.Errorf("projection: unknown attribute %q on %s", name, info.TypeName)
		}
		if seen[name] {
			return nil, fmt.Errorf("projection: duplicate attribute %q on %s", name, info.TypeName)
		}
		seen[name] = true
		fields = append(fields, field)
	}
	return fields, nil
}

// reservedVars lists the variables that clauses writes, so the filter
// compiler never allocates them (issue #138).
func (p projectionPlan) reservedVars() []string {
	vars := make([]string, 0, 1+2*len(p.roles))
	vars = append(vars, "projection_type")
	for i := range p.roles {
		vars = append(vars, fmt.Sprintf("projection_role_%d", i), fmt.Sprintf("projection_role_type_%d", i))
	}
	return vars
}

func (p projectionPlan) clauses() (string, string) {
	match := []string{"$e isa! $projection_type;"}
	items := []string{`"_iid": iid($e)`, `"_type": label($projection_type)`}
	items = appendFetchProjectionItems(items, p.rootFields, "e")
	for i, role := range p.roles {
		roleVar := fmt.Sprintf("projection_role_%d", i)
		typeVar := fmt.Sprintf("projection_role_type_%d", i)
		match = append(match,
			fmt.Sprintf("$e links (%s: $%s);", role.name, roleVar),
			fmt.Sprintf("$%s isa! $%s;", roleVar, typeVar),
		)
		subItems := []string{
			fmt.Sprintf(`"_iid": iid($%s)`, roleVar),
			fmt.Sprintf(`"_type": label($%s)`, typeVar),
		}
		subItems = appendFetchProjectionItems(subItems, role.fields, roleVar)
		items = append(items, fmt.Sprintf(`"%s": { %s }`, role.name, strings.Join(subItems, ", ")))
	}
	return strings.Join(match, "\n"), "fetch { " + strings.Join(items, ", ") + " };"
}

func (p projectionPlan) decode(row map[string]any) (ProjectedResult, error) {
	root, err := decodeProjectedNode(row, p.rootFields)
	if err != nil {
		return ProjectedResult{}, err
	}
	if len(p.roles) != 0 {
		root.Roles = make(map[string]*ProjectedResult, len(p.roles))
	}
	for _, role := range p.roles {
		value, ok := lookupResultValue(row, role.name)
		if !ok || value == nil {
			return ProjectedResult{}, fmt.Errorf("projection role %q: missing result", role.name)
		}
		roleRow, ok := value.(map[string]any)
		if !ok {
			return ProjectedResult{}, fmt.Errorf("projection role %q: result is %T, not an object", role.name, value)
		}
		player, err := decodeProjectedNode(roleRow, role.fields)
		if err != nil {
			return ProjectedResult{}, fmt.Errorf("projection role %q: %w", role.name, err)
		}
		root.Roles[role.name] = &player
	}
	return root, nil
}

func decodeProjectedNode(row map[string]any, fields []FieldInfo) (ProjectedResult, error) {
	iid, ok := lookupResultValue(row, "_iid")
	if !ok {
		return ProjectedResult{}, fmt.Errorf("projection: missing _iid")
	}
	iidString, ok := iid.(string)
	if !ok || iidString == "" {
		return ProjectedResult{}, fmt.Errorf("projection: invalid _iid %T", iid)
	}
	typeValue, ok := lookupResultValue(row, "_type")
	if !ok {
		return ProjectedResult{}, fmt.Errorf("projection: missing _type")
	}
	typeName, ok := typeValue.(string)
	if !ok || typeName == "" {
		return ProjectedResult{}, fmt.Errorf("projection: invalid _type %T", typeValue)
	}
	result := ProjectedResult{IID: iidString, TypeName: typeName, Fields: make(map[string]ProjectedField, len(fields))}
	for _, field := range fields {
		value, found := lookupResultValue(row, field.Tag.Name)
		result.Fields[field.Tag.Name] = ProjectedField{Present: found && value != nil, Value: value}
	}
	return result, nil
}

// GetProjected reads selected attributes without creating mutable model values.
// The filter keys and projection fields use TypeDB attribute names.
func (m *Manager[T]) GetProjected(ctx context.Context, filters map[string]any, spec Projection) ([]ProjectedResult, error) {
	plan, err := planProjection(m.info, spec)
	if err != nil {
		return nil, err
	}
	match, err := m.buildFilteredMatch("e", filters)
	if err != nil {
		return nil, err
	}
	additions, fetch := plan.clauses()
	return m.readProjected(ctx, match+"\n"+additions+"\n"+fetch, plan)
}

func (m *Manager[T]) readProjected(ctx context.Context, query string, plan projectionPlan) ([]ProjectedResult, error) {
	rows, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	results := make([]ProjectedResult, 0, len(rows))
	for _, row := range rows {
		result, err := plan.decode(row)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// ExecuteProjected runs a fluent read with selected attributes. It preserves
// the query's filters, order, offset, and limit. The result is not a model.
func (q *Query[T]) ExecuteProjected(ctx context.Context, spec Projection) ([]ProjectedResult, error) {
	plan, err := planProjection(q.mgr.info, spec)
	if err != nil {
		return nil, err
	}
	additions, fetch := plan.clauses()
	query, err := q.buildQueryWithFetch(additions, fetch, plan.reservedVars()...)
	if err != nil {
		return nil, err
	}
	return q.mgr.readProjected(ctx, query, plan)
}
