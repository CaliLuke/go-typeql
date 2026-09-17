package tqlgen

import (
 "fmt"
 "testing"
)

var releaseParseResult *ParsedSchema

func BenchmarkReleaseParseSchema(b *testing.B) {
 for _,blocks:=range []int{1,20}{
  schema:="define\n"
  for i:=range blocks{schema+=fmt.Sprintf("attribute name-%d, value string; entity person-%d, owns name-%d @key;\n",i,i,i)}
  b.Run(fmt.Sprintf("blocks_%d",blocks),func(b *testing.B){
   if _,err:=ParseSchema(schema);err!=nil{b.Fatal(err)}
   b.ReportAllocs();b.ResetTimer()
   for range b.N{var err error;releaseParseResult,err=ParseSchema(schema);if err!=nil{b.Fatal(err)}}
  })
 }
}
