import Scopes
import Lean.Data.Json

set_option autoImplicit false

/-!
# Differential test driver for issue #138

Reads a JSON array of builder specs on stdin and prints, for each spec, the
occurrences of `emit designCand` as a JSON array. The Go test
`gotype/compile_diff_test.go` compares these with the Go compiler.

Filter JSON: `{"cmp": label}`, `{"computed": expr}`, `{"role": [label, filter]}`,
`{"and": [filter, filter]}`, `{"or": [filter, filter]}`, `{"not": filter}`.
Expression JSON: `{"attr": label}`, `"lit"`, `{"bin": [expr, expr]}`.
Spec JSON: `{"filter": filter, "sorts": [label], "reduces": [label], "olds": n}`.
-/

open Lean Scopes Allocator

def toName (s : String) : Allocator.Name := s.toList.map Char.toNat
def fromName (n : Allocator.Name) : String := String.ofList (n.map Char.ofNat)

partial def parseExpr (j : Json) : Except String Expr := do
  if let .ok s := j.getStr? then
    if s == "lit" then return .lit else throw s!"unknown expression {s}"
  if let .ok l := j.getObjValAs? String "attr" then return .attr (toName l)
  let args ← (← j.getObjVal? "bin").getArr?
  if args.size != 2 then throw "bin takes two expressions"
  return .bin (← parseExpr args[0]!) (← parseExpr args[1]!)

partial def parseF (j : Json) : Except String F := do
  if let .ok l := j.getObjValAs? String "cmp" then return .cmp (toName l)
  if let .ok e := j.getObjVal? "computed" then return .computed (← parseExpr e)
  if let .ok f := j.getObjVal? "not" then return .not (← parseF f)
  if let .ok a := j.getObjVal? "role" then
    let a ← a.getArr?
    return .role (toName (← a[0]!.getStr?)) (← parseF a[1]!)
  if let .ok a := j.getObjVal? "and" then
    let a ← a.getArr?
    return .and (← parseF a[0]!) (← parseF a[1]!)
  let a ← (← j.getObjVal? "or").getArr?
  return .or (← parseF a[0]!) (← parseF a[1]!)

def parseSpec (j : Json) : Except String Spec := do
  let f ← parseF (← j.getObjVal? "filter")
  let sorts ← (← j.getObjValAs? (Array String) "sorts").toList.map toName |> pure
  let reduces ← (← j.getObjValAs? (Array String) "reduces").toList.map toName |> pure
  let olds ← j.getObjValAs? Nat "olds"
  return ⟨f, sorts, reduces, olds⟩

def famName : Fam → String
  | .root => "root" | .attr => "attr" | .player => "player"
  | .result => "computed" | .reduce => "reduce" | .old => "old"

def kindName : Kind → String
  | .bind => "bind" | .use => "use" | .ownerUse => "ownerUse"

def occJson (o : Occ) : Json :=
  Json.mkObj [("n", fromName o.n), ("fam", famName o.fam), ("scope", o.scope), ("kind", kindName o.kind)]

def main : IO UInt32 := do
  let input ← (← IO.getStdin).readToEnd
  match Json.parse input >>= (·.getArr?) with
  | .error e => IO.eprintln s!"diffmodel: {e}"; return 1
  | .ok specs =>
    let mut out : Array Json := #[]
    for s in specs do
      match parseSpec s with
      | .error e => IO.eprintln s!"diffmodel: {e}"; return 1
      | .ok q => out := out.push (Json.arr ((emit designCand q).map occJson).toArray)
    IO.println (Json.arr out).compress
    return 0
