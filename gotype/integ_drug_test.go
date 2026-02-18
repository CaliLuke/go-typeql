//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"math"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Drug discovery domain models
// ---------------------------------------------------------------------------

type Compound struct {
	gotype.BaseEntity
	CompoundID      string  `typedb:"compound-id,key"`
	CompoundName    string  `typedb:"compound-name"`
	MolecularWeight float64 `typedb:"molecular-weight"`
	Solubility      float64 `typedb:"solubility"`
}

type Target struct {
	gotype.BaseEntity
	TargetID   string `typedb:"target-id,key"`
	TargetName string `typedb:"target-name"`
	Organism   string `typedb:"organism"`
}

type Disease struct {
	gotype.BaseEntity
	DiseaseID   string `typedb:"disease-id,key"`
	DiseaseName string `typedb:"disease-name"`
	Category    string `typedb:"disease-category"`
}

type Trial struct {
	gotype.BaseEntity
	TrialID    string  `typedb:"trial-id,key"`
	Phase      int     `typedb:"phase"`
	SuccessRate float64 `typedb:"success-rate"`
}

type InteractsWith struct {
	gotype.BaseRelation
	Ligand     *Compound `typedb:"role:ligand"`
	Receptor   *Target   `typedb:"role:receptor"`
	Affinity   float64   `typedb:"affinity"`
}

type Treats struct {
	gotype.BaseRelation
	Treatment  *Compound `typedb:"role:treatment"`
	Condition  *Disease  `typedb:"role:condition"`
}

type TestedIn struct {
	gotype.BaseRelation
	TestedCompound *Compound `typedb:"role:tested-compound"`
	ClinicalTrial  *Trial    `typedb:"role:clinical-trial"`
	Dosage         float64   `typedb:"dosage-mg"`
}

type TargetDisease struct {
	gotype.BaseRelation
	DiseaseTarget   *Target  `typedb:"role:disease-target"`
	TargetedDisease *Disease `typedb:"role:targeted-disease"`
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func setupDrugDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[Compound]()
		_ = gotype.Register[Target]()
		_ = gotype.Register[Disease]()
		_ = gotype.Register[Trial]()
		_ = gotype.Register[InteractsWith]()
		_ = gotype.Register[Treats]()
		_ = gotype.Register[TestedIn]()
		_ = gotype.Register[TargetDisease]()
	})
}

type drugFixture struct {
	db        *gotype.Database
	compounds []*Compound
	targets   []*Target
	diseases  []*Disease
	trials    []*Trial
}

func seedDrug(t *testing.T) drugFixture {
	t.Helper()
	db := setupDrugDB(t)
	ctx := context.Background()

	compoundMgr := gotype.NewManager[Compound](db)
	targetMgr := gotype.NewManager[Target](db)
	diseaseMgr := gotype.NewManager[Disease](db)
	trialMgr := gotype.NewManager[Trial](db)
	interactMgr := gotype.NewManager[InteractsWith](db)
	treatsMgr := gotype.NewManager[Treats](db)
	testedMgr := gotype.NewManager[TestedIn](db)
	tdMgr := gotype.NewManager[TargetDisease](db)

	compounds := []*Compound{
		{CompoundID: "CPD-001", CompoundName: "Aspirin", MolecularWeight: 180.16, Solubility: 4.6},
		{CompoundID: "CPD-002", CompoundName: "Ibuprofen", MolecularWeight: 206.29, Solubility: 0.021},
		{CompoundID: "CPD-003", CompoundName: "Metformin", MolecularWeight: 129.16, Solubility: 300.0},
		{CompoundID: "CPD-004", CompoundName: "Atorvastatin", MolecularWeight: 558.64, Solubility: 0.00063},
	}
	assertInsertMany(t, ctx, compoundMgr, compounds)
	for i, c := range compounds {
		compounds[i] = assertGetOne(t, ctx, compoundMgr, map[string]any{"compound-id": c.CompoundID})
	}

	targets := []*Target{
		{TargetID: "TGT-001", TargetName: "COX-1", Organism: "Human"},
		{TargetID: "TGT-002", TargetName: "COX-2", Organism: "Human"},
		{TargetID: "TGT-003", TargetName: "AMPK", Organism: "Human"},
		{TargetID: "TGT-004", TargetName: "HMG-CoA", Organism: "Human"},
	}
	assertInsertMany(t, ctx, targetMgr, targets)
	for i, tg := range targets {
		targets[i] = assertGetOne(t, ctx, targetMgr, map[string]any{"target-id": tg.TargetID})
	}

	diseases := []*Disease{
		{DiseaseID: "DIS-001", DiseaseName: "Inflammation", Category: "Autoimmune"},
		{DiseaseID: "DIS-002", DiseaseName: "Diabetes Type 2", Category: "Metabolic"},
		{DiseaseID: "DIS-003", DiseaseName: "Hypercholesterolemia", Category: "Cardiovascular"},
	}
	assertInsertMany(t, ctx, diseaseMgr, diseases)
	for i, d := range diseases {
		diseases[i] = assertGetOne(t, ctx, diseaseMgr, map[string]any{"disease-id": d.DiseaseID})
	}

	trials := []*Trial{
		{TrialID: "TRL-001", Phase: 3, SuccessRate: 0.85},
		{TrialID: "TRL-002", Phase: 2, SuccessRate: 0.60},
		{TrialID: "TRL-003", Phase: 3, SuccessRate: 0.92},
		{TrialID: "TRL-004", Phase: 1, SuccessRate: 0.45},
	}
	assertInsertMany(t, ctx, trialMgr, trials)
	for i, tr := range trials {
		trials[i] = assertGetOne(t, ctx, trialMgr, map[string]any{"trial-id": tr.TrialID})
	}

	// Interactions: Aspirin→COX-1, Aspirin→COX-2, Ibuprofen→COX-2, Metformin→AMPK, Atorvastatin→HMG-CoA
	interactData := []struct{ c, t int; a float64 }{
		{0, 0, 8.5}, {0, 1, 7.2}, {1, 1, 9.1}, {2, 2, 6.8}, {3, 3, 9.5},
	}
	for _, id := range interactData {
		assertInsert(t, ctx, interactMgr, &InteractsWith{
			Ligand: compounds[id.c], Receptor: targets[id.t], Affinity: id.a,
		})
	}

	// Treats: Aspirin→Inflammation, Ibuprofen→Inflammation, Metformin→Diabetes, Atorvastatin→Hypercholesterolemia
	treatsData := []struct{ c, d int }{
		{0, 0}, {1, 0}, {2, 1}, {3, 2},
	}
	for _, td := range treatsData {
		assertInsert(t, ctx, treatsMgr, &Treats{Treatment: compounds[td.c], Condition: diseases[td.d]})
	}

	// Tested: Aspirin→TRL-001(100mg), Ibuprofen→TRL-002(200mg), Metformin→TRL-003(500mg), Atorvastatin→TRL-004(10mg)
	testedData := []struct{ c, t int; d float64 }{
		{0, 0, 100.0}, {1, 1, 200.0}, {2, 2, 500.0}, {3, 3, 10.0},
	}
	for _, td := range testedData {
		assertInsert(t, ctx, testedMgr, &TestedIn{
			TestedCompound: compounds[td.c], ClinicalTrial: trials[td.t], Dosage: td.d,
		})
	}

	// Target→Disease: COX-1→Inflammation, COX-2→Inflammation, AMPK→Diabetes, HMG-CoA→Hypercholesterolemia
	tdData := []struct{ tg, d int }{
		{0, 0}, {1, 0}, {2, 1}, {3, 2},
	}
	for _, d := range tdData {
		assertInsert(t, ctx, tdMgr, &TargetDisease{DiseaseTarget: targets[d.tg], TargetedDisease: diseases[d.d]})
	}

	return drugFixture{db: db, compounds: compounds, targets: targets, diseases: diseases, trials: trials}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_Drug_AllEntitiesInserted(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[Compound](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[Target](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[Disease](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Trial](f.db), 4)
}

func TestIntegration_Drug_AllRelationsInserted(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[InteractsWith](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[Treats](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[TestedIn](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[TargetDisease](f.db), 4)
}

func TestIntegration_Drug_FilterByMolecularWeight(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Compound](f.db)

	// Small molecules (MW < 250)
	results, err := mgr.Query().
		Filter(gotype.Lt("molecular-weight", 250.0)).
		OrderAsc("molecular-weight").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Metformin(129.16), Aspirin(180.16), Ibuprofen(206.29)
	if len(results) != 3 {
		t.Fatalf("expected 3 small molecules, got %d", len(results))
	}
	if results[0].CompoundName != "Metformin" {
		t.Errorf("expected Metformin first, got %q", results[0].CompoundName)
	}
}

func TestIntegration_Drug_FilterHighSolubility(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Compound](f.db)

	results, err := mgr.Query().
		Filter(gotype.Gt("solubility", 1.0)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Aspirin(4.6), Metformin(300)
	if len(results) != 2 {
		t.Errorf("expected 2 highly soluble compounds, got %d", len(results))
	}
}

func TestIntegration_Drug_FilterTrialsByPhase(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Trial](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("phase", 3)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 phase-3 trials, got %d", len(results))
	}
}

func TestIntegration_Drug_AggregateAvgSuccessRate(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Trial](f.db)

	avg, err := mgr.Query().Avg("success-rate").Execute(ctx)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	// (0.85+0.60+0.92+0.45)/4 = 0.705
	if math.Abs(avg-0.705) > 0.01 {
		t.Errorf("expected avg success rate ~0.705, got %f", avg)
	}
}

func TestIntegration_Drug_MaxSuccessRate(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Trial](f.db)

	max, err := mgr.Query().Max("success-rate").Execute(ctx)
	if err != nil {
		t.Fatalf("max: %v", err)
	}
	if math.Abs(max-0.92) > 0.01 {
		t.Errorf("expected max success rate 0.92, got %f", max)
	}
}

func TestIntegration_Drug_FilterTargetsByOrganism(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Target](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("organism", "Human")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("expected 4 human targets, got %d", len(results))
	}
}

func TestIntegration_Drug_FilterDiseasesByCategory(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Disease](f.db)

	results, err := mgr.Query().
		Filter(gotype.Neq("disease-category", "Autoimmune")).
		OrderAsc("disease-name").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 non-autoimmune diseases, got %d", len(results))
	}
}

func TestIntegration_Drug_UpdateCompound(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Compound](f.db)

	compound := assertGetOne(t, ctx, mgr, map[string]any{"compound-id": "CPD-001"})
	compound.Solubility = 5.0
	assertUpdate(t, ctx, mgr, compound)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"compound-id": "CPD-001"})
	if math.Abs(updated.Solubility-5.0) > 0.01 {
		t.Errorf("expected solubility 5.0, got %f", updated.Solubility)
	}
}

func TestIntegration_Drug_CompoundNameContains(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Compound](f.db)

	results, err := mgr.Query().
		Filter(gotype.Contains("compound-name", "in")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Aspirin, Metformin, Atorvastatin all contain "in"
	if len(results) != 3 {
		t.Errorf("expected 3 compounds containing 'in', got %d", len(results))
	}
}

func TestIntegration_Drug_RangeMolecularWeight(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Compound](f.db)

	results, err := mgr.Query().
		Filter(gotype.Range("molecular-weight", 150.0, 250.0)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Aspirin(180.16), Ibuprofen(206.29)
	if len(results) != 2 {
		t.Errorf("expected 2 compounds in MW range 150-250, got %d", len(results))
	}
}

func TestIntegration_Drug_CountInteractions(t *testing.T) {
	f := seedDrug(t)
	ctx := context.Background()
	mgr := gotype.NewManager[InteractsWith](f.db)

	count, err := mgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 interactions, got %d", count)
	}
}
