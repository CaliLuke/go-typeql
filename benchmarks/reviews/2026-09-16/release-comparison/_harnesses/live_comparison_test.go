//go:build cgo && typedb && integration

package gotype

import (
 "context"
 "fmt"
 "testing"
)

func BenchmarkReleaseComparison(b *testing.B) {
 f:=liveBenchSetup(b)
 ctx:=context.Background()
 sequence:=0
 people:=func(n int) []*liveBenchPerson {
  sequence++
  rows:=make([]*liveBenchPerson,n)
  for i:=range rows {
   name:=fmt.Sprintf("release-compare-%d-%d",sequence,i)
   rows[i]=&liveBenchPerson{Name:name,Email:name+"@example.test",Age:30}
  }
  return rows
 }
 cases:=[]struct{name string; run func() error}{
  {"ReadByIID",func()error {_,err:=f.personMgr.GetByIID(ctx,f.personIID);return err}},
  {"ReadByKey",func()error {rows,err:=f.personMgr.Get(ctx,map[string]any{"name":"person-00"});if err==nil&&len(rows)!=1{return fmt.Errorf("got %d rows",len(rows))};return err}},
  {"GetOne",func()error {_,err:=f.personMgr.GetOne(ctx,map[string]any{"name":"person-00"});return err}},
  {"All256",func()error {rows,err:=f.personMgr.All(ctx);if err==nil&&len(rows)!=256{return fmt.Errorf("got %d rows",len(rows))};return err}},
  {"WithRoles256",func()error {rows,err:=f.employMgr.GetWithRoles(ctx,nil);if err==nil&&len(rows)!=256{return fmt.Errorf("got %d rows",len(rows))};return err}},
  {"ExistsBroad256",func()error {ok,err:=f.personMgr.Query().Filter(Gte("age",0)).Exists(ctx);if err==nil&&!ok{return fmt.Errorf("missing existing rows")};return err}},
  {"PutOne",func()error {p:=people(1)[0];if err:=f.personMgr.Put(ctx,p);err!=nil{return err};if p.GetIID()==""{return fmt.Errorf("missing IID")};return nil}},
  {"Insert16",func()error {rows:=people(16);if err:=f.personMgr.InsertMany(ctx,rows);err!=nil{return err};for _,p:=range rows{if p.GetIID()==""{return fmt.Errorf("missing IID")}};return nil}},
  {"Put16",func()error {rows:=people(16);for i:=0;i<16;i+=2{j:=i/2;rows[i]=&liveBenchPerson{Name:fmt.Sprintf("person-%02d",j),Email:fmt.Sprintf("person-%02d@example.test",j),Age:int64(30+j)}};if err:=f.personMgr.PutMany(ctx,rows);err!=nil{return err};for _,p:=range rows{if p.GetIID()==""{return fmt.Errorf("missing IID")}};return nil}},
 }
 for _,tc:=range cases {
  b.Run(tc.name,func(b *testing.B){b.ReportAllocs();for range b.N{if err:=tc.run();err!=nil{b.Fatal(err)}}})
 }
 b.Run("Update16",func(b *testing.B){
  b.StopTimer();rows:=people(16)
  for _,p:=range rows{if err:=f.personMgr.Insert(ctx,p);err!=nil{b.Fatal(err)}}
  b.ReportAllocs();b.StartTimer()
  for iteration:=range b.N{for i,p:=range rows{p.Age=int64(iteration*16+i+100)};if err:=f.personMgr.UpdateMany(ctx,rows);err!=nil{b.Fatal(err)}}
 })
 for _,strict:=range []bool{false,true}{
  name:="Delete16";if strict{name="Delete16Strict"}
  b.Run(name,func(b *testing.B){
   b.ReportAllocs()
   for range b.N {
    b.StopTimer();rows:=people(16)
    for _,p:=range rows{if err:=f.personMgr.Insert(ctx,p);err!=nil{b.Fatal(err)}}
    b.StartTimer()
    var err error
    if strict{err=f.personMgr.DeleteMany(ctx,rows,WithStrict())}else{err=f.personMgr.DeleteMany(ctx,rows)}
    if err!=nil{b.Fatal(err)}
   }
  })
 }
}
