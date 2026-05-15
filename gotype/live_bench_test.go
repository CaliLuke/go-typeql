//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/driver"
)

type liveBenchPerson struct {
	BaseEntity
	Name  string `typedb:"name,key"`
	Email string `typedb:"email,unique"`
	Age   int64  `typedb:"age"`
}

type liveBenchCompany struct {
	BaseEntity
	Name string `typedb:"company-name,key"`
}

type liveBenchEmployment struct {
	BaseRelation
	Employee *liveBenchPerson  `typedb:"role:employee"`
	Employer *liveBenchCompany `typedb:"role:employer"`
	Since    int64             `typedb:"since"`
}

type liveBenchDriverAdapter struct {
	drv *driver.Driver
}

func (a *liveBenchDriverAdapter) Transaction(dbName string, txType int) (Tx, error) {
	tx, err := a.drv.Transaction(dbName, driver.TransactionType(txType))
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (a *liveBenchDriverAdapter) Schema(dbName string) (string, error) {
	return a.drv.Databases().Schema(dbName)
}

func (a *liveBenchDriverAdapter) DatabaseCreate(name string) error {
	return a.drv.Databases().Create(name)
}

func (a *liveBenchDriverAdapter) DatabaseDelete(name string) error {
	return a.drv.Databases().Delete(name)
}

func (a *liveBenchDriverAdapter) DatabaseContains(name string) (bool, error) {
	return a.drv.Databases().Contains(name)
}

func (a *liveBenchDriverAdapter) DatabaseAll() ([]string, error) {
	return a.drv.Databases().All()
}

func (a *liveBenchDriverAdapter) Close() {
	a.drv.Close()
}

func (a *liveBenchDriverAdapter) IsOpen() bool {
	return a.drv.IsOpen()
}

type liveBenchFixture struct {
	dbName      string
	drv         *driver.Driver
	db          *Database
	personMgr   *Manager[liveBenchPerson]
	companyMgr  *Manager[liveBenchCompany]
	employMgr   *Manager[liveBenchEmployment]
	person      *liveBenchPerson
	personIID   string
	company     *liveBenchCompany
	employment  *liveBenchEmployment
	rawIIDQuery string
}

var liveBenchOnce sync.Once
var liveBenchData *liveBenchFixture
var liveBenchErr error

func TestMain(m *testing.M) {
	code := m.Run()
	if liveBenchData != nil {
		_ = liveBenchData.drv.Databases().Delete(liveBenchData.dbName)
		liveBenchData.drv.Close()
	}
	os.Exit(code)
}

func liveBenchAddress() string {
	if addr := os.Getenv("TEST_DB_ADDRESS"); addr != "" {
		return addr
	}
	return "localhost:1730"
}

func liveBenchSetup(b testing.TB) *liveBenchFixture {
	b.Helper()
	liveBenchOnce.Do(func() {
		liveBenchData, liveBenchErr = newLiveBenchFixture()
	})
	if liveBenchErr != nil {
		b.Fatalf("live bench setup: %v", liveBenchErr)
	}
	return liveBenchData
}

func newLiveBenchFixture() (*liveBenchFixture, error) {
	ClearRegistry()
	if err := Register[liveBenchPerson](); err != nil {
		return nil, err
	}
	if err := Register[liveBenchCompany](); err != nil {
		return nil, err
	}
	if err := Register[liveBenchEmployment](); err != nil {
		return nil, err
	}

	drv, err := driver.OpenWithTLS(liveBenchAddress(), "admin", "password", false, "")
	if err != nil {
		return nil, err
	}

	dbName := fmt.Sprintf("bench_async_close_%d", time.Now().UnixNano())
	dm := drv.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		drv.Close()
		return nil, err
	}

	db := NewDatabase(&liveBenchDriverAdapter{drv: drv}, dbName)
	if err := db.ExecuteSchema(context.Background(), GenerateSchema()); err != nil {
		_ = dm.Delete(dbName)
		drv.Close()
		return nil, err
	}

	f := &liveBenchFixture{
		dbName:     dbName,
		drv:        drv,
		db:         db,
		personMgr:  mustLiveBenchManager[liveBenchPerson](db),
		companyMgr: mustLiveBenchManager[liveBenchCompany](db),
		employMgr:  mustLiveBenchManager[liveBenchEmployment](db),
	}
	if err := f.seed(context.Background()); err != nil {
		_ = dm.Delete(dbName)
		drv.Close()
		return nil, err
	}
	return f, nil
}

func mustLiveBenchManager[T any](db *Database) *Manager[T] {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	info, ok := LookupType(t)
	if !ok {
		panic(fmt.Sprintf("unregistered live benchmark type %s", t.Name()))
	}
	return &Manager[T]{
		db:       db,
		info:     info,
		strategy: strategyFor(info.Kind),
	}
}

func (f *liveBenchFixture) seed(ctx context.Context) error {
	companies := make([]*liveBenchCompany, 5)
	for i := range companies {
		c := &liveBenchCompany{Name: fmt.Sprintf("company-%02d", i)}
		if err := f.companyMgr.Insert(ctx, c); err != nil {
			return err
		}
		companies[i] = c
	}
	f.company = companies[0]

	for i := 0; i < 25; i++ {
		p := &liveBenchPerson{
			Name:  fmt.Sprintf("person-%02d", i),
			Email: fmt.Sprintf("person-%02d@example.test", i),
			Age:   int64(30 + i),
		}
		if err := f.personMgr.Insert(ctx, p); err != nil {
			return err
		}
		if i == 0 {
			f.person = p
			f.personIID = p.GetIID()
		}
		e := &liveBenchEmployment{
			Employee: p,
			Employer: companies[i%len(companies)],
			Since:    int64(2000 + i),
		}
		if err := f.employMgr.Insert(ctx, e); err != nil {
			return err
		}
		if i == 0 {
			f.employment = e
		}
	}

	f.rawIIDQuery = fmt.Sprintf(`match
$e isa live-bench-person, iid %s;
fetch {
  "name": $e.name,
  "email": $e.email,
  "age": $e.age
};`, f.personIID)
	return nil
}

func BenchmarkLiveRead_GetByIID(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := f.personMgr.GetByIID(ctx, f.personIID)
		if err != nil {
			b.Fatal(err)
		}
		if got == nil {
			b.Fatal("GetByIID returned nil")
		}
	}
}

func BenchmarkLiveRead_Get(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	filter := map[string]any{"name": f.person.Name}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := f.personMgr.Get(ctx, filter)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 1 {
			b.Fatalf("got %d results", len(got))
		}
	}
}

func BenchmarkLiveRead_All(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := f.personMgr.All(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("All returned no results")
		}
	}
}

func BenchmarkLiveRead_GetWithRoles(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	filter := map[string]any{"since": f.employment.Since}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := f.employMgr.GetWithRoles(ctx, filter)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 1 {
			b.Fatalf("got %d results", len(got))
		}
	}
}

func BenchmarkLiveRead_CloseOnly(b *testing.B) {
	f := liveBenchSetup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := f.db.Transaction(ReadTransaction)
		if err != nil {
			b.Fatal(err)
		}
		tx.Close()
	}
}

func BenchmarkLiveRead_CloseCheckedOnly(b *testing.B) {
	f := liveBenchSetup(b)
	b.ReportAllocs()
	tx, err := f.db.Transaction(ReadTransaction)
	if err != nil {
		b.Fatal(err)
	}
	checked, ok := tx.(interface{ CloseChecked() error })
	if !ok {
		tx.Close()
		b.Skip("CloseChecked not implemented")
	}
	if err := checked.CloseChecked(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := f.db.Transaction(ReadTransaction)
		if err != nil {
			b.Fatal(err)
		}
		if err := tx.(interface{ CloseChecked() error }).CloseChecked(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLiveRead_GetByIIDBreakdown(b *testing.B) {
	f := liveBenchSetup(b)
	var openTotal, queryTotal, closeTotal time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		tx, err := f.db.Transaction(ReadTransaction)
		openTotal += time.Since(start)
		if err != nil {
			b.Fatal(err)
		}

		start = time.Now()
		results, err := tx.Query(f.rawIIDQuery)
		queryTotal += time.Since(start)
		if err != nil {
			b.Fatal(err)
		}
		if len(results) != 1 {
			b.Fatalf("got %d raw results", len(results))
		}

		start = time.Now()
		tx.Close()
		closeTotal += time.Since(start)
	}
	b.StopTimer()
	n := float64(b.N)
	b.ReportMetric(float64(openTotal.Nanoseconds())/n, "open-ns/op")
	b.ReportMetric(float64(queryTotal.Nanoseconds())/n, "query-ns/op")
	b.ReportMetric(float64(closeTotal.Nanoseconds())/n, "close-ns/op")
	total := openTotal + queryTotal + closeTotal
	if total > 0 {
		b.ReportMetric(100*float64(closeTotal)/float64(total), "close-pct")
	}
}

func BenchmarkLiveClose_ChannelEnqueue(b *testing.B) {
	type closeJob struct {
		id int
	}
	jobs := make(chan closeJob, 1024)
	done := make(chan struct{})
	go func() {
		for range jobs {
		}
		close(done)
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jobs <- closeJob{id: i}
	}
	b.StopTimer()
	close(jobs)
	<-done
}

func BenchmarkLiveClose_GoroutinePerClose(b *testing.B) {
	var wg sync.WaitGroup
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func() {
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestLiveRead_SameConnectionOverlapStress(t *testing.T) {
	if testing.Short() {
		t.Skip("integration stress")
	}
	f := liveBenchSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if err := ctx.Err(); err != nil {
					errs <- err
					return
				}
				tx, err := f.db.Transaction(ReadTransaction)
				if err != nil {
					errs <- err
					return
				}
				if _, err := tx.Query(f.rawIIDQuery); err != nil {
					tx.Close()
					errs <- err
					return
				}
				tx.Close()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !strings.Contains(err.Error(), "context deadline") {
			t.Fatal(err)
		}
	}
}
