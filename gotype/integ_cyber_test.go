//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Cyber threat intelligence domain models
// ---------------------------------------------------------------------------

type ThreatActor struct {
	gotype.BaseEntity
	ActorID     string `typedb:"actor-id,key"`
	ActorAlias  string `typedb:"actor-alias"`
	Origin      string `typedb:"origin"`
	Sophistication int `typedb:"sophistication"`
}

type Malware struct {
	gotype.BaseEntity
	MalwareID   string `typedb:"malware-id,key"`
	MalwareName string `typedb:"malware-name"`
	MalwareType string `typedb:"malware-type"`
}

type Vulnerability struct {
	gotype.BaseEntity
	CveID    string  `typedb:"cve-id,key"`
	Severity float64 `typedb:"severity"`
	VulnDesc string  `typedb:"vuln-desc"`
}

type Indicator struct {
	gotype.BaseEntity
	IndicatorID    string `typedb:"indicator-id,key"`
	IndicatorType  string `typedb:"indicator-type"`
	IndicatorValue string `typedb:"indicator-value"`
}

type Campaign struct {
	gotype.BaseEntity
	CampaignID   string `typedb:"campaign-id,key"`
	CampaignName string `typedb:"campaign-name"`
	StartYear    int    `typedb:"start-year"`
}

type UsesMalware struct {
	gotype.BaseRelation
	MalwareUser *ThreatActor `typedb:"role:malware-user"`
	UsedMalware *Malware     `typedb:"role:used-malware"`
}

type Exploits struct {
	gotype.BaseRelation
	Exploiter     *Malware       `typedb:"role:exploiter"`
	ExploitedVuln *Vulnerability `typedb:"role:exploited-vuln"`
}

type IndicatesActor struct {
	gotype.BaseRelation
	ThreatIndicator *Indicator   `typedb:"role:threat-indicator"`
	IndicatedActor  *ThreatActor `typedb:"role:indicated-actor"`
}

type AttributedTo struct {
	gotype.BaseRelation
	AttributedCampaign *Campaign    `typedb:"role:attributed-campaign"`
	AttributedActor    *ThreatActor `typedb:"role:attributed-actor"`
}

type CampaignUsesIndicator struct {
	gotype.BaseRelation
	UsingCampaign *Campaign  `typedb:"role:using-campaign"`
	UsedIndicator *Indicator `typedb:"role:used-indicator"`
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func setupCyberDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[ThreatActor]()
		_ = gotype.Register[Malware]()
		_ = gotype.Register[Vulnerability]()
		_ = gotype.Register[Indicator]()
		_ = gotype.Register[Campaign]()
		_ = gotype.Register[UsesMalware]()
		_ = gotype.Register[Exploits]()
		_ = gotype.Register[IndicatesActor]()
		_ = gotype.Register[AttributedTo]()
		_ = gotype.Register[CampaignUsesIndicator]()
	})
}

type cyberFixture struct {
	db      *gotype.Database
	actors  []*ThreatActor
	malware []*Malware
	vulns   []*Vulnerability
	indicators []*Indicator
	campaigns  []*Campaign
}

func seedCyber(t *testing.T) cyberFixture {
	t.Helper()
	db := setupCyberDB(t)
	ctx := context.Background()

	actorMgr := gotype.NewManager[ThreatActor](db)
	malwareMgr := gotype.NewManager[Malware](db)
	vulnMgr := gotype.NewManager[Vulnerability](db)
	indicatorMgr := gotype.NewManager[Indicator](db)
	campaignMgr := gotype.NewManager[Campaign](db)
	usesMgr := gotype.NewManager[UsesMalware](db)
	exploitsMgr := gotype.NewManager[Exploits](db)
	indicatesMgr := gotype.NewManager[IndicatesActor](db)
	attrMgr := gotype.NewManager[AttributedTo](db)
	campIndMgr := gotype.NewManager[CampaignUsesIndicator](db)

	actors := []*ThreatActor{
		{ActorID: "APT-28", ActorAlias: "Fancy Bear", Origin: "Russia", Sophistication: 9},
		{ActorID: "APT-29", ActorAlias: "Cozy Bear", Origin: "Russia", Sophistication: 10},
		{ActorID: "APT-41", ActorAlias: "Double Dragon", Origin: "China", Sophistication: 8},
	}
	assertInsertMany(t, ctx, actorMgr, actors)
	for i, a := range actors {
		actors[i] = assertGetOne(t, ctx, actorMgr, map[string]any{"actor-id": a.ActorID})
	}

	malware := []*Malware{
		{MalwareID: "MAL-001", MalwareName: "X-Agent", MalwareType: "RAT"},
		{MalwareID: "MAL-002", MalwareName: "WellMess", MalwareType: "Backdoor"},
		{MalwareID: "MAL-003", MalwareName: "ShadowPad", MalwareType: "Backdoor"},
		{MalwareID: "MAL-004", MalwareName: "Mimikatz", MalwareType: "Credential Dumper"},
	}
	assertInsertMany(t, ctx, malwareMgr, malware)
	for i, m := range malware {
		malware[i] = assertGetOne(t, ctx, malwareMgr, map[string]any{"malware-id": m.MalwareID})
	}

	vulns := []*Vulnerability{
		{CveID: "CVE-2021-34527", Severity: 8.8, VulnDesc: "PrintNightmare"},
		{CveID: "CVE-2021-44228", Severity: 10.0, VulnDesc: "Log4Shell"},
		{CveID: "CVE-2023-23397", Severity: 9.8, VulnDesc: "Outlook Elevation"},
	}
	assertInsertMany(t, ctx, vulnMgr, vulns)
	for i, v := range vulns {
		vulns[i] = assertGetOne(t, ctx, vulnMgr, map[string]any{"cve-id": v.CveID})
	}

	indicators := []*Indicator{
		{IndicatorID: "IOC-001", IndicatorType: "IP", IndicatorValue: "185.100.87.202"},
		{IndicatorID: "IOC-002", IndicatorType: "Domain", IndicatorValue: "evil.example.com"},
		{IndicatorID: "IOC-003", IndicatorType: "Hash", IndicatorValue: "abc123def456"},
		{IndicatorID: "IOC-004", IndicatorType: "IP", IndicatorValue: "203.0.113.42"},
	}
	assertInsertMany(t, ctx, indicatorMgr, indicators)
	for i, ind := range indicators {
		indicators[i] = assertGetOne(t, ctx, indicatorMgr, map[string]any{"indicator-id": ind.IndicatorID})
	}

	campaigns := []*Campaign{
		{CampaignID: "CAMP-001", CampaignName: "SolarWinds", StartYear: 2020},
		{CampaignID: "CAMP-002", CampaignName: "NotPetya", StartYear: 2017},
		{CampaignID: "CAMP-003", CampaignName: "Operation Cloud Hopper", StartYear: 2016},
	}
	assertInsertMany(t, ctx, campaignMgr, campaigns)
	for i, c := range campaigns {
		campaigns[i] = assertGetOne(t, ctx, campaignMgr, map[string]any{"campaign-id": c.CampaignID})
	}

	// Uses: APT-28→X-Agent, APT-28→Mimikatz, APT-29→WellMess, APT-41→ShadowPad, APT-41→Mimikatz
	usesData := []struct{ a, m int }{
		{0, 0}, {0, 3}, {1, 1}, {2, 2}, {2, 3},
	}
	for _, u := range usesData {
		assertInsert(t, ctx, usesMgr, &UsesMalware{MalwareUser: actors[u.a], UsedMalware: malware[u.m]})
	}

	// Exploits: X-Agent→PrintNightmare, WellMess→Log4Shell, ShadowPad→Outlook
	exploitsData := []struct{ m, v int }{
		{0, 0}, {1, 1}, {2, 2},
	}
	for _, e := range exploitsData {
		assertInsert(t, ctx, exploitsMgr, &Exploits{Exploiter: malware[e.m], ExploitedVuln: vulns[e.v]})
	}

	// Indicates: IOC-001→APT-28, IOC-002→APT-29, IOC-003→APT-41, IOC-004→APT-28
	indicatesData := []struct{ i, a int }{
		{0, 0}, {1, 1}, {2, 2}, {3, 0},
	}
	for _, id := range indicatesData {
		assertInsert(t, ctx, indicatesMgr, &IndicatesActor{ThreatIndicator: indicators[id.i], IndicatedActor: actors[id.a]})
	}

	// Attribution: SolarWinds→APT-29, NotPetya→APT-28, CloudHopper→APT-41
	for i := range campaigns {
		assertInsert(t, ctx, attrMgr, &AttributedTo{AttributedCampaign: campaigns[i], AttributedActor: actors[i]})
	}

	// Campaign indicators: SolarWinds→IOC-001,IOC-002; NotPetya→IOC-001,IOC-004; CloudHopper→IOC-003
	campIndData := []struct{ c, i int }{
		{0, 0}, {0, 1}, {1, 0}, {1, 3}, {2, 2},
	}
	for _, ci := range campIndData {
		assertInsert(t, ctx, campIndMgr, &CampaignUsesIndicator{UsingCampaign: campaigns[ci.c], UsedIndicator: indicators[ci.i]})
	}

	return cyberFixture{db: db, actors: actors, malware: malware, vulns: vulns, indicators: indicators, campaigns: campaigns}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_Cyber_AllEntitiesInserted(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[ThreatActor](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Malware](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[Vulnerability](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Indicator](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[Campaign](f.db), 3)
}

func TestIntegration_Cyber_AllRelationsInserted(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[UsesMalware](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[Exploits](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[IndicatesActor](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[AttributedTo](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[CampaignUsesIndicator](f.db), 5)
}

func TestIntegration_Cyber_FilterActorsByOrigin(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[ThreatActor](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("origin", "Russia")).
		OrderAsc("actor-id").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 Russian actors, got %d", len(results))
	}
	if results[0].ActorID != "APT-28" {
		t.Errorf("expected APT-28, got %q", results[0].ActorID)
	}
}

func TestIntegration_Cyber_FilterHighSophistication(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[ThreatActor](f.db)

	results, err := mgr.Query().
		Filter(gotype.Gte("sophistication", 9)).
		OrderDesc("sophistication").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 highly sophisticated actors, got %d", len(results))
	}
	if results[0].ActorAlias != "Cozy Bear" {
		t.Errorf("expected Cozy Bear (10) first, got %q", results[0].ActorAlias)
	}
}

func TestIntegration_Cyber_FilterCriticalVulns(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Vulnerability](f.db)

	results, err := mgr.Query().
		Filter(gotype.Gte("severity", 9.5)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Log4Shell(10.0), Outlook(9.8)
	if len(results) != 2 {
		t.Errorf("expected 2 critical vulns, got %d", len(results))
	}
}

func TestIntegration_Cyber_FilterMalwareByType(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Malware](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("malware-type", "Backdoor")).
		OrderAsc("malware-name").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 backdoors, got %d", len(results))
	}
}

func TestIntegration_Cyber_FilterIndicatorsByType(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Indicator](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("indicator-type", "IP")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 IP indicators, got %d", len(results))
	}
}

func TestIntegration_Cyber_CampaignsByStartYear(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Campaign](f.db)

	results, err := mgr.Query().
		OrderAsc("start-year").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 campaigns, got %d", len(results))
	}
	if results[0].CampaignName != "Operation Cloud Hopper" {
		t.Errorf("expected Cloud Hopper (2016) first, got %q", results[0].CampaignName)
	}
	if results[2].CampaignName != "SolarWinds" {
		t.Errorf("expected SolarWinds (2020) last, got %q", results[2].CampaignName)
	}
}

func TestIntegration_Cyber_MaxSeverity(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Vulnerability](f.db)

	max, err := mgr.Query().Max("severity").Execute(ctx)
	if err != nil {
		t.Fatalf("max: %v", err)
	}
	if max != 10.0 {
		t.Errorf("expected max severity 10.0, got %f", max)
	}
}

func TestIntegration_Cyber_AvgSophistication(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[ThreatActor](f.db)

	avg, err := mgr.Query().Avg("sophistication").Execute(ctx)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	// (9+10+8)/3 = 9.0
	if avg < 8.9 || avg > 9.1 {
		t.Errorf("expected avg sophistication 9.0, got %f", avg)
	}
}

func TestIntegration_Cyber_ContainsSearch(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[ThreatActor](f.db)

	results, err := mgr.Query().
		Filter(gotype.Contains("actor-alias", "Bear")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 *Bear actors, got %d", len(results))
	}
}

func TestIntegration_Cyber_InFilterMalware(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Malware](f.db)

	results, err := mgr.Query().
		Filter(gotype.In("malware-name", []any{"X-Agent", "Mimikatz"})).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 specific malware, got %d", len(results))
	}
}

func TestIntegration_Cyber_UpdateVulnSeverity(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Vulnerability](f.db)

	vuln := assertGetOne(t, ctx, mgr, map[string]any{"cve-id": "CVE-2021-34527"})
	vuln.Severity = 9.0
	assertUpdate(t, ctx, mgr, vuln)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"cve-id": "CVE-2021-34527"})
	if updated.Severity != 9.0 {
		t.Errorf("expected severity 9.0, got %f", updated.Severity)
	}
}

func TestIntegration_Cyber_RecentCampaigns(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Campaign](f.db)

	results, err := mgr.Query().
		Filter(gotype.Gte("start-year", 2018)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// SolarWinds (2020) only
	if len(results) != 1 {
		t.Errorf("expected 1 recent campaign, got %d", len(results))
	}
}

func TestIntegration_Cyber_OrFilterActors(t *testing.T) {
	f := seedCyber(t)
	ctx := context.Background()
	mgr := gotype.NewManager[ThreatActor](f.db)

	// Russian OR sophistication >= 10
	results, err := mgr.Query().
		Filter(gotype.Or(
			gotype.Eq("origin", "China"),
			gotype.Gte("sophistication", 10),
		)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// APT-29 (Cozy Bear, soph=10) + APT-41 (China, soph=8) = 2
	if len(results) != 2 {
		t.Errorf("expected 2 actors from OR filter, got %d", len(results))
	}
}
