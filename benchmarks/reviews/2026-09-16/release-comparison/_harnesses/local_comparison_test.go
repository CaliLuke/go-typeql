package gotype

import "testing"

func BenchmarkReleaseFetchProjection(b *testing.B) {
 ClearRegistry();MustRegister[testPerson]()
 info,_:=LookupType(typeOf[testPerson]())
 if _,err:=buildFetchAll(info,"e");err!=nil{b.Fatal(err)}
 b.ReportAllocs();b.ResetTimer()
 for range b.N{if _,err:=buildFetchAll(info,"e");err!=nil{b.Fatal(err)}}
}
