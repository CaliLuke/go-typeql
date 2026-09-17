//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/v2/driver"
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

const liveBenchPersonCount = 256

type liveBenchDriverAdapter struct {
	drv *driver.Driver
}

type liveBenchCountConn struct {
	Conn
	queries *atomic.Int64
}

func (c *liveBenchCountConn) Transaction(name string, mode int) (Tx, error) {
	tx, err := c.Conn.Transaction(name, mode)
	if err != nil {
		return nil, err
	}
	return &liveBenchCountTx{Tx: tx, queries: c.queries}, nil
}

type liveBenchCountTx struct {
	Tx
	queries *atomic.Int64
}

func (t *liveBenchCountTx) Query(query string) ([]map[string]any, error) {
	t.queries.Add(1)
	return t.Tx.Query(query)
}

func (t *liveBenchCountTx) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	t.queries.Add(1)
	return t.Tx.QueryWithContext(ctx, query)
}

func (t *liveBenchCountTx) QueryEachWithContext(ctx context.Context, query string, fn func(int, map[string]any) error) error {
	t.queries.Add(1)
	if rowTx, ok := t.Tx.(rowQueryTx); ok {
		return rowTx.QueryEachWithContext(ctx, query, fn)
	}
	rows, err := t.Tx.QueryWithContext(ctx, query)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := fn(len(rows), row); err != nil {
			return err
		}
	}
	return nil
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

	for i := range liveBenchPersonCount {
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

// BenchmarkLiveExists compares bounded existence checks with full distinct
// counts. The fixture is expanded between groups, outside the timed loops.
// Run explicitly against the compose server with -bench '^BenchmarkLiveExists$'
// and -count=1 in five independent processes; its fixture grows during each
// run, so repeated benchmarks in one process would reuse the expanded data.
func BenchmarkLiveExists(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	seeded := liveBenchPersonCount
	for _, size := range []int{256, 512, 1000} {
		for i := seeded; i < size; i++ {
			p := &liveBenchPerson{
				Name:  fmt.Sprintf("person-%05d", i),
				Email: fmt.Sprintf("person-%05d@example.test", i),
				Age:   30,
			}
			if err := f.personMgr.Insert(ctx, p); err != nil {
				b.Fatalf("seed person %d: %v", i, err)
			}
		}
		seeded = size
		for _, workload := range []struct {
			name   string
			filter Filter
			found  bool
		}{
			{"absent", Eq("name", "not-present"), false},
			{"unique", Eq("name", "person-00"), true},
			{"broad", Gte("age", 0), true},
		} {
			b.Run(fmt.Sprintf("rows=%d/%s/exists", size, workload.name), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					got, err := f.personMgr.Query().Filter(workload.filter).Exists(ctx)
					if err != nil || got != workload.found {
						b.Fatalf("Exists = %v, %v; want %v", got, err, workload.found)
					}
				}
			})
			b.Run(fmt.Sprintf("rows=%d/%s/count", size, workload.name), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					got, err := f.personMgr.Query().Filter(workload.filter).Count(ctx)
					if err != nil || (got > 0) != workload.found {
						b.Fatalf("Count = %d, %v; expected match %v", got, err, workload.found)
					}
				}
			})
		}
	}
}

func BenchmarkLiveRead_GetByIID(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	var queries atomic.Int64
	mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(&liveBenchCountConn{Conn: f.db.GetConn(), queries: &queries}, f.dbName))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := mgr.GetByIID(ctx, f.personIID)
		if err != nil {
			b.Fatal(err)
		}
		if got == nil {
			b.Fatal("GetByIID returned nil")
		}
	}
	b.ReportMetric(float64(queries.Load())/float64(b.N), "queries/op")
}

// BenchmarkLiveReadScope includes the transaction open and caller-visible
// close in both cases. The shared case serializes reads on one native
// transaction; it does not measure asynchronous native close-drain time.
func BenchmarkLiveReadScope(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for _, reads := range []int{2, 5, 10} {
		b.Run(fmt.Sprintf("reads=%d/fresh", reads), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				for j := range reads {
					if j%2 == 0 {
						if p, err := f.personMgr.GetByIID(ctx, f.personIID); err != nil || p == nil {
							b.Fatalf("fresh person = %v, %v", p, err)
						}
					} else if c, err := f.companyMgr.GetByIID(ctx, f.company.GetIID()); err != nil || c == nil {
						b.Fatalf("fresh company = %v, %v", c, err)
					}
				}
			}
		})
		b.Run(fmt.Sprintf("reads=%d/shared", reads), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				scope, err := f.db.BeginContext(ctx, ReadTransaction)
				if err != nil {
					b.Fatal(err)
				}
				persons := MustNewManagerWithTx[liveBenchPerson](scope)
				companies := MustNewManagerWithTx[liveBenchCompany](scope)
				for j := range reads {
					if j%2 == 0 {
						if p, err := persons.GetByIID(ctx, f.personIID); err != nil || p == nil {
							scope.Close()
							b.Fatalf("shared person = %v, %v", p, err)
						}
					} else if c, err := companies.GetByIID(ctx, f.company.GetIID()); err != nil || c == nil {
						scope.Close()
						b.Fatalf("shared company = %v, %v", c, err)
					}
				}
				scope.Close()
			}
		})
	}
}

// BenchmarkLiveReadScopePerRead excludes the shared transaction's initial
// open and final close to show why per-read-only comparisons are optimistic
// for short scopes. Fresh reads still open and close their own transactions.
func BenchmarkLiveReadScopePerRead(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	b.Run("fresh", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			if i%2 == 0 {
				if p, err := f.personMgr.GetByIID(ctx, f.personIID); err != nil || p == nil {
					b.Fatalf("fresh person = %v, %v", p, err)
				}
			} else if c, err := f.companyMgr.GetByIID(ctx, f.company.GetIID()); err != nil || c == nil {
				b.Fatalf("fresh company = %v, %v", c, err)
			}
		}
	})
	b.Run("shared", func(b *testing.B) {
		scope, err := f.db.BeginContext(ctx, ReadTransaction)
		if err != nil {
			b.Fatal(err)
		}
		persons := MustNewManagerWithTx[liveBenchPerson](scope)
		companies := MustNewManagerWithTx[liveBenchCompany](scope)
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			if i%2 == 0 {
				if p, err := persons.GetByIID(ctx, f.personIID); err != nil || p == nil {
					scope.Close()
					b.Fatalf("shared person = %v, %v", p, err)
				}
			} else if c, err := companies.GetByIID(ctx, f.company.GetIID()); err != nil || c == nil {
				scope.Close()
				b.Fatalf("shared company = %v, %v", c, err)
			}
		}
		b.StopTimer()
		scope.Close()
	})
}

func BenchmarkLiveRead_Get(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	var queries atomic.Int64
	mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(&liveBenchCountConn{Conn: f.db.GetConn(), queries: &queries}, f.dbName))
	filter := map[string]any{"name": f.person.Name}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := mgr.Get(ctx, filter)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 1 {
			b.Fatalf("got %d results", len(got))
		}
	}
	b.ReportMetric(float64(queries.Load())/float64(b.N), "queries/op")
}

func BenchmarkLiveRead_All(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	var queries atomic.Int64
	mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(&liveBenchCountConn{Conn: f.db.GetConn(), queries: &queries}, f.dbName))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := mgr.All(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("All returned no results")
		}
	}
	b.ReportMetric(float64(queries.Load())/float64(b.N), "queries/op")
}

func BenchmarkLiveRead_GetWithRoles(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	var queries atomic.Int64
	mgr := mustLiveBenchManager[liveBenchEmployment](NewDatabase(&liveBenchCountConn{Conn: f.db.GetConn(), queries: &queries}, f.dbName))
	filter := map[string]any{"since": f.employment.Since}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := mgr.GetWithRoles(ctx, filter)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 1 {
			b.Fatalf("got %d results", len(got))
		}
	}
	b.ReportMetric(float64(queries.Load())/float64(b.N), "queries/op")
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
	b.ReportMetric(1, "queries/op")
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
