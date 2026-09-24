import Allocator

set_option autoImplicit false

/-!
# Scopes and filter compilation for issue #138

A model of the filter compiler: filter trees, scope contexts (rule R2), and
the allocator of `Allocator.lean`. The compiler emits every variable
occurrence with its scope. Theorems about the output hold for every filter
tree.

Check with: `cd formal/lean && lake build`
-/

namespace Scopes

open Allocator

/-! ## Keys, occurrences, filters -/

inductive Fam where
  | root | attr | player | result | reduce | old
  deriving DecidableEq

structure Key where
  fam : Fam
  owner : Name
  label : Name
  scope : Nat
  deriving DecidableEq

/-- `bind`: the occurrence binds the variable (`has`, `links`, `let`, `isa`).
`use`: the occurrence reads a variable of its own scope.
`ownerUse`: the occurrence reads the owner (`$owner has ...`, `$owner links ...`). -/
inductive Kind where
  | bind | use | ownerUse
  deriving DecidableEq

structure Occ where
  n : Name
  fam : Fam
  scope : Nat
  path : List Nat
  kind : Kind

inductive Expr where
  | attr (label : Name)
  | lit
  | bin (a b : Expr)

/-- Filters: a comparison on an attribute of the owner, `Computed`, `RolePlayer`,
binary `And` and `Or`, and `Not`. -/
inductive F where
  | cmp (label : Name)
  | computed (e : Expr)
  | role (r : Name) (f : F)
  | and (a b : F)
  | or (a b : F)
  | not (f : F)

/-- The owner, its family, the current scope, and the path of scopes
(the current scope first, then the enclosing scopes). -/
structure Ctx where
  owner : Name
  ownerFam : Fam
  scope : Nat
  path : List Nat

structure CS where
  tbl : State Key
  next : Nat

variable (cand : Key → Name)

def nm (cs : CS) (k : Key) : Name × CS :=
  ((alloc cand cs.tbl k).1, { cs with tbl := (alloc cand cs.tbl k).2 })

def ownerOcc (ctx : Ctx) : Occ := ⟨ctx.owner, ctx.ownerFam, ctx.scope, ctx.path, .ownerUse⟩
def occ (ctx : Ctx) (n : Name) (fam : Fam) (k : Kind) : Occ := ⟨n, fam, ctx.scope, ctx.path, k⟩

def attrKey (ctx : Ctx) (l : Name) : Key := ⟨.attr, ctx.owner, l, ctx.scope⟩

/-- An attribute reference: `$owner has l $v;`, then a use of `$v`. -/
def compileExpr (ctx : Ctx) : Expr → CS → List Occ × CS
  | .attr l, cs =>
    let r := nm cand cs (attrKey ctx l)
    ([ownerOcc ctx, occ ctx r.1 .attr .bind, occ ctx r.1 .attr .use], r.2)
  | .lit, cs => ([], cs)
  | .bin a b, cs =>
    let r1 := compileExpr ctx a cs
    let r2 := compileExpr ctx b r1.2
    (r1.1 ++ r2.1, r2.2)

def childCtx (ctx : Ctx) (s : Nat) : Ctx := { ctx with scope := s, path := s :: ctx.path }

def compile : Ctx → F → CS → List Occ × CS
  | ctx, .cmp l, cs =>
    let r := nm cand cs (attrKey ctx l)
    ([ownerOcc ctx, occ ctx r.1 .attr .bind, occ ctx r.1 .attr .use], r.2)
  | ctx, .computed e, cs =>
    let r1 := compileExpr cand ctx e cs
    let id := r1.2.next
    let r2 := nm cand { r1.2 with next := id + 1 } ⟨.result, [], digits id, ctx.scope⟩
    (r1.1 ++ [occ ctx r2.1 .result .bind, occ ctx r2.1 .result .use], r2.2)
  | ctx, .role rl f, cs =>
    let r1 := nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩
    let r2 := compile { ctx with owner := r1.1, ownerFam := .player } f r1.2
    ([ownerOcc ctx, occ ctx r1.1 .player .bind] ++ r2.1, r2.2)
  | ctx, .and a b, cs =>
    let r1 := compile ctx a cs
    let r2 := compile ctx b r1.2
    (r1.1 ++ r2.1, r2.2)
  | ctx, .or a b, cs =>
    let s := cs.next
    let r1 := compile (childCtx ctx s) a { cs with next := s + 2 }
    let r2 := compile (childCtx ctx (s + 1)) b r1.2
    (r1.1 ++ r2.1, r2.2)
  | ctx, .not a, cs =>
    let s := cs.next
    compile (childCtx ctx s) a { cs with next := s + 1 }

/-- A query: allocate the root in scope 0, bind it (`$e isa T`), then compile. -/
def compileQuery (f : F) : List Occ × CS :=
  let r0 := nm cand ⟨⟨[]⟩, 1⟩ ⟨.root, [], [], 0⟩
  let ctx : Ctx := ⟨r0.1, .root, 0, [0]⟩
  let r := compile cand ctx f r0.2
  (occ ctx r0.1 .root .bind :: r.1, r.2)

/-! ## The table only grows, and the invariant holds -/

theorem nm_grow (cs : CS) (k : Key) :
    ∃ ext, (nm cand cs k).2.tbl.table = ext ++ cs.tbl.table := by
  unfold nm alloc
  cases cs.tbl.table.lookup k with
  | some _ => exact ⟨[], rfl⟩
  | none => exact ⟨[_], rfl⟩

theorem nm_next (cs : CS) (k : Key) : (nm cand cs k).2.next = cs.next := rfl

theorem nm_inv (cs : CS) (k : Key) (h : Inv cs.tbl) : Inv (nm cand cs k).2.tbl :=
  alloc_inv cand cs.tbl k h

theorem mem_of_lookup : ∀ (t : List (Key × Name)) (k : Key) (n : Name),
    t.lookup k = some n → (k, n) ∈ t
  | [], _, _, h => by simp [List.lookup] at h
  | (k', n') :: t, k, n, h => by
    by_cases hk : k = k'
    · subst hk; simp [List.lookup] at h; subst h; simp
    · have hk' : (k == k') = false := by simp [hk]
      simp only [List.lookup, hk'] at h
      exact List.mem_cons_of_mem _ (mem_of_lookup t k n h)

theorem nm_mem (cs : CS) (k : Key) : (k, (nm cand cs k).1) ∈ (nm cand cs k).2.tbl.table :=
  mem_of_lookup _ k _ (alloc_memo cand cs.tbl k)

structure Grows (cs cs' : CS) : Prop where
  ext : ∃ e, cs'.tbl.table = e ++ cs.tbl.table
  inv : Inv cs.tbl → Inv cs'.tbl
  next : cs.next ≤ cs'.next

theorem Grows.refl (cs : CS) : Grows cs cs := ⟨⟨[], rfl⟩, id, Nat.le_refl _⟩

theorem Grows.trans {a b c : CS} (h1 : Grows a b) (h2 : Grows b c) : Grows a c := by
  obtain ⟨e1, h1e⟩ := h1.ext
  obtain ⟨e2, h2e⟩ := h2.ext
  exact ⟨⟨e2 ++ e1, by rw [h2e, h1e, List.append_assoc]⟩, fun h => h2.inv (h1.inv h),
    Nat.le_trans h1.next h2.next⟩

theorem nm_grows (cs : CS) (k : Key) : Grows cs (nm cand cs k).2 :=
  ⟨nm_grow cand cs k, nm_inv cand cs k, by rw [nm_next]; exact Nat.le_refl _⟩

theorem bump_grows (cs : CS) (m : Nat) (h : cs.next ≤ m) : Grows cs { cs with next := m } :=
  ⟨⟨[], rfl⟩, id, h⟩

theorem Grows.mem {a b : CS} (h : Grows a b) {p : Key × Name} (hp : p ∈ a.tbl.table) :
    p ∈ b.tbl.table := by
  obtain ⟨e, he⟩ := h.ext
  rw [he]; exact List.mem_append_right _ hp

theorem expr_grows (ctx : Ctx) : ∀ (e : Expr) (cs : CS), Grows cs (compileExpr cand ctx e cs).2
  | .attr _, cs => nm_grows cand cs _
  | .lit, cs => Grows.refl cs
  | .bin a b, cs => (expr_grows ctx a cs).trans (expr_grows ctx b _)

theorem compile_grows : ∀ (ctx : Ctx) (f : F) (cs : CS), Grows cs (compile cand ctx f cs).2
  | ctx, .cmp l, cs => nm_grows cand cs _
  | ctx, .computed e, cs => by
    simp only [compile]
    exact (expr_grows cand ctx e cs).trans
      ((bump_grows _ _ (Nat.le_succ _)).trans (nm_grows cand _ _))
  | ctx, .role rl f, cs => by
    simp only [compile]
    exact (nm_grows cand cs _).trans (compile_grows _ f _)
  | ctx, .and a b, cs => by
    simp only [compile]
    exact (compile_grows ctx a cs).trans (compile_grows ctx b _)
  | ctx, .or a b, cs => by
    simp only [compile]
    exact ((bump_grows _ _ (by omega)).trans (compile_grows _ a _)).trans (compile_grows _ b _)
  | ctx, .not a, cs => by
    simp only [compile]
    exact (bump_grows _ _ (by omega)).trans (compile_grows _ a _)

/-! ## Every occurrence is backed by a table entry

An occurrence is backed if the table maps a key of the same family to its
name, and, unless it is an owner use, that key has the scope of the
occurrence. -/

def Backed (t : List (Key × Name)) (o : Occ) : Prop :=
  ∃ k, (k, o.n) ∈ t ∧ k.fam = o.fam ∧ (o.kind ≠ .ownerUse → k.scope = o.scope)

def OwnerBacked (t : List (Key × Name)) (ctx : Ctx) : Prop :=
  ∃ k, (k, ctx.owner) ∈ t ∧ k.fam = ctx.ownerFam

theorem Backed.mono {a b : CS} (h : Grows a b) {o : Occ} (ho : Backed a.tbl.table o) :
    Backed b.tbl.table o := by
  obtain ⟨k, hk, h1, h2⟩ := ho
  exact ⟨k, h.mem hk, h1, h2⟩

theorem OwnerBacked.mono {a b : CS} (h : Grows a b) {ctx : Ctx} (ho : OwnerBacked a.tbl.table ctx) :
    OwnerBacked b.tbl.table ctx := by
  obtain ⟨k, hk, h1⟩ := ho
  exact ⟨k, h.mem hk, h1⟩

theorem ownerOcc_backed (t : List (Key × Name)) (ctx : Ctx) (h : OwnerBacked t ctx) :
    Backed t (ownerOcc ctx) := by
  obtain ⟨k, hk, h1⟩ := h
  exact ⟨k, hk, h1, fun hne => absurd rfl hne⟩

theorem attr_backed (cs : CS) (ctx : Ctx) (l : Name) (k : Kind) :
    Backed (nm cand cs (attrKey ctx l)).2.tbl.table (occ ctx (nm cand cs (attrKey ctx l)).1 .attr k) :=
  ⟨attrKey ctx l, nm_mem cand cs _, rfl, fun _ => rfl⟩

theorem expr_backed (ctx : Ctx) : ∀ (e : Expr) (cs : CS), OwnerBacked cs.tbl.table ctx →
    ∀ o ∈ (compileExpr cand ctx e cs).1, Backed (compileExpr cand ctx e cs).2.tbl.table o
  | .attr l, cs, hob, o, ho => by
    simp only [compileExpr, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl | rfl
    · exact ownerOcc_backed _ ctx (hob.mono (nm_grows cand cs _))
    · exact attr_backed cand cs ctx l _
    · exact attr_backed cand cs ctx l _
  | .lit, cs, _, o, ho => by simp [compileExpr] at ho
  | .bin a b, cs, hob, o, ho => by
    simp only [compileExpr, List.mem_append] at ho
    have g2 := expr_grows cand ctx b (compileExpr cand ctx a cs).2
    rcases ho with ho | ho
    · exact (expr_backed ctx a cs hob o ho).mono g2
    · exact expr_backed ctx b _ (hob.mono (expr_grows cand ctx a cs)) o ho

theorem compile_backed : ∀ (ctx : Ctx) (f : F) (cs : CS), OwnerBacked cs.tbl.table ctx →
    ∀ o ∈ (compile cand ctx f cs).1, Backed (compile cand ctx f cs).2.tbl.table o
  | ctx, .cmp l, cs, hob, o, ho => by
    simp only [compile, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl | rfl
    · exact ownerOcc_backed _ ctx (hob.mono (nm_grows cand cs _))
    · exact attr_backed cand cs ctx l _
    · exact attr_backed cand cs ctx l _
  | ctx, .computed e, cs, hob, o, ho => by
    simp only [compile, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    have g := (bump_grows (compileExpr cand ctx e cs).2 ((compileExpr cand ctx e cs).2.next + 1)
      (Nat.le_succ _)).trans (nm_grows cand _ ⟨.result, [], digits (compileExpr cand ctx e cs).2.next, ctx.scope⟩)
    rcases ho with ho | rfl | rfl
    · exact (expr_backed cand ctx e cs hob o ho).mono g
    · exact ⟨_, nm_mem cand _ _, rfl, fun _ => rfl⟩
    · exact ⟨_, nm_mem cand _ _, rfl, fun _ => rfl⟩
  | ctx, .role rl f, cs, hob, o, ho => by
    simp only [compile, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    have g1 := nm_grows cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩
    have g2 := compile_grows cand
      { ctx with owner := (nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩).1, ownerFam := .player }
      f (nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩).2
    rcases ho with (rfl | rfl) | ho
    · exact (ownerOcc_backed _ ctx (hob.mono g1)).mono g2
    · exact Backed.mono g2 ⟨_, nm_mem cand _ _, rfl, fun _ => rfl⟩
    · exact compile_backed _ f _ ⟨_, nm_mem cand _ _, rfl⟩ o ho
  | ctx, .and a b, cs, hob, o, ho => by
    simp only [compile, List.mem_append] at ho
    rcases ho with ho | ho
    · exact (compile_backed ctx a cs hob o ho).mono (compile_grows cand ctx b _)
    · exact compile_backed ctx b _ (hob.mono (compile_grows cand ctx a cs)) o ho
  | ctx, .or a b, cs, hob, o, ho => by
    simp only [compile, List.mem_append] at ho
    let cs0 : CS := { cs with next := cs.next + 2 }
    let r1 := compile cand (childCtx ctx cs.next) a cs0
    have hob0 : OwnerBacked cs0.tbl.table (childCtx ctx cs.next) := hob
    have hob1 : OwnerBacked r1.2.tbl.table (childCtx ctx (cs.next + 1)) :=
      (show OwnerBacked cs0.tbl.table (childCtx ctx (cs.next + 1)) from hob).mono
        (compile_grows cand (childCtx ctx cs.next) a cs0)
    rcases ho with ho | ho
    · exact (compile_backed (childCtx ctx cs.next) a cs0 hob0 o ho).mono
        (compile_grows cand (childCtx ctx (cs.next + 1)) b r1.2)
    · exact compile_backed (childCtx ctx (cs.next + 1)) b r1.2 hob1 o ho
  | ctx, .not a, cs, hob, o, ho => by
    simp only [compile] at ho
    exact compile_backed (childCtx ctx cs.next) a { cs with next := cs.next + 1 }
      (show OwnerBacked _ (childCtx ctx cs.next) from hob) o ho

/-! ## Uses are bound in their own scope -/

def UsesBound (out : List Occ) : Prop :=
  ∀ o ∈ out, o.kind = .use → ∃ b ∈ out, b.kind = .bind ∧ b.n = o.n ∧ b.scope = o.scope

theorem UsesBound.append {a b : List Occ} (ha : UsesBound a) (hb : UsesBound b) : UsesBound (a ++ b) := by
  intro o ho hk
  rcases List.mem_append.mp ho with h | h
  · obtain ⟨x, hx, h1⟩ := ha o h hk; exact ⟨x, List.mem_append_left _ hx, h1⟩
  · obtain ⟨x, hx, h1⟩ := hb o h hk; exact ⟨x, List.mem_append_right _ hx, h1⟩

theorem attr_triple_bound (ctx : Ctx) (v : Name) :
    UsesBound [ownerOcc ctx, occ ctx v .attr .bind, occ ctx v .attr .use] := by
  intro o ho hk
  simp only [List.mem_cons, List.not_mem_nil, or_false] at ho
  rcases ho with rfl | rfl | rfl
  · simp [ownerOcc] at hk
  · simp [occ] at hk
  · exact ⟨occ ctx v .attr .bind, by simp, rfl, rfl, rfl⟩

theorem expr_uses_bound (ctx : Ctx) : ∀ (e : Expr) (cs : CS), UsesBound (compileExpr cand ctx e cs).1
  | .attr l, cs => attr_triple_bound ctx _
  | .lit, cs => by intro o ho; simp [compileExpr] at ho
  | .bin a b, cs => (expr_uses_bound ctx a cs).append (expr_uses_bound ctx b _)

theorem compile_uses_bound : ∀ (ctx : Ctx) (f : F) (cs : CS), UsesBound (compile cand ctx f cs).1
  | ctx, .cmp l, cs => attr_triple_bound ctx _
  | ctx, .computed e, cs => by
    simp only [compile]
    apply (expr_uses_bound cand ctx e cs).append
    intro o ho hk
    simp only [List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl
    · simp [occ] at hk
    · exact ⟨occ ctx _ .result .bind, by simp, rfl, rfl, rfl⟩
  | ctx, .role rl f, cs => by
    simp only [compile]
    apply UsesBound.append _ (compile_uses_bound _ f _)
    intro o ho hk
    simp only [List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl <;> simp [ownerOcc, occ] at hk
  | ctx, .and a b, cs => by
    simp only [compile]; exact (compile_uses_bound ctx a cs).append (compile_uses_bound ctx b _)
  | ctx, .or a b, cs => by
    simp only [compile]; exact (compile_uses_bound _ a _).append (compile_uses_bound _ b _)
  | ctx, .not a, cs => by
    simp only [compile]; exact compile_uses_bound _ a _

/-! ## Paths and scope ranges -/

def WF (ctx : Ctx) : Prop := ctx.path.head? = some ctx.scope

def PathOK (ctx : Ctx) (out : List Occ) : Prop :=
  ∀ o ∈ out, (∃ t, o.path = t ++ ctx.path) ∧ o.path.head? = some o.scope

theorem PathOK.append {ctx : Ctx} {a b : List Occ} (ha : PathOK ctx a) (hb : PathOK ctx b) :
    PathOK ctx (a ++ b) := by
  intro o ho
  rcases List.mem_append.mp ho with h | h
  · exact ha o h
  · exact hb o h

theorem PathOK.child {ctx : Ctx} {s : Nat} {out : List Occ} (h : PathOK (childCtx ctx s) out) :
    PathOK ctx out := by
  intro o ho
  obtain ⟨⟨t, ht⟩, hh⟩ := h o ho
  exact ⟨⟨t ++ [s], by rw [ht]; simp [childCtx]⟩, hh⟩

theorem local_path (ctx : Ctx) (hw : WF ctx) (l : List Occ)
    (hl : ∀ o ∈ l, o.scope = ctx.scope ∧ o.path = ctx.path) : PathOK ctx l := by
  intro o ho
  obtain ⟨h1, h2⟩ := hl o ho
  exact ⟨⟨[], by simp [h2]⟩, by rw [h2, h1]; exact hw⟩

theorem expr_path (ctx : Ctx) (hw : WF ctx) : ∀ (e : Expr) (cs : CS), PathOK ctx (compileExpr cand ctx e cs).1
  | .attr l, cs => local_path ctx hw _ (by
      intro o ho; simp only [compileExpr, List.mem_cons, List.not_mem_nil, or_false] at ho
      rcases ho with rfl | rfl | rfl <;> exact ⟨rfl, rfl⟩)
  | .lit, cs => by intro o ho; simp [compileExpr] at ho
  | .bin a b, cs => (expr_path ctx hw a cs).append (expr_path ctx hw b _)

theorem compile_path : ∀ (ctx : Ctx) (f : F) (cs : CS), WF ctx → PathOK ctx (compile cand ctx f cs).1
  | ctx, .cmp l, cs, hw => local_path ctx hw _ (by
      intro o ho; simp only [compile, List.mem_cons, List.not_mem_nil, or_false] at ho
      rcases ho with rfl | rfl | rfl <;> exact ⟨rfl, rfl⟩)
  | ctx, .computed e, cs, hw => by
    simp only [compile]
    apply (expr_path cand ctx hw e cs).append
    apply local_path ctx hw
    intro o ho; simp only [List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl <;> exact ⟨rfl, rfl⟩
  | ctx, .role rl f, cs, hw => by
    simp only [compile]
    apply PathOK.append
    · apply local_path ctx hw
      intro o ho; simp only [List.mem_cons, List.not_mem_nil, or_false] at ho
      rcases ho with rfl | rfl <;> exact ⟨rfl, rfl⟩
    · exact compile_path { ctx with owner := _, ownerFam := .player } f _ hw
  | ctx, .and a b, cs, hw => by
    simp only [compile]; exact (compile_path ctx a cs hw).append (compile_path ctx b _ hw)
  | ctx, .or a b, cs, hw => by
    simp only [compile]
    exact (compile_path _ a _ (by simp [WF, childCtx])).child.append
      (compile_path _ b _ (by simp [WF, childCtx])).child
  | ctx, .not a, cs, hw => by
    simp only [compile]; exact (compile_path _ a _ (by simp [WF, childCtx])).child

def InRange (ctx : Ctx) (lo hi : Nat) (out : List Occ) : Prop :=
  ∀ o ∈ out, o.scope = ctx.scope ∨ (lo ≤ o.scope ∧ o.scope < hi)

theorem InRange.append {ctx : Ctx} {lo hi : Nat} {a b : List Occ} (ha : InRange ctx lo hi a)
    (hb : InRange ctx lo hi b) : InRange ctx lo hi (a ++ b) := by
  intro o ho
  rcases List.mem_append.mp ho with h | h
  · exact ha o h
  · exact hb o h

theorem InRange.widen {ctx : Ctx} {lo hi lo' hi' : Nat} {a : List Occ} (h : InRange ctx lo hi a)
    (h1 : lo' ≤ lo) (h2 : hi ≤ hi') : InRange ctx lo' hi' a := by
  intro o ho
  rcases h o ho with e | ⟨x, y⟩
  · exact Or.inl e
  · exact Or.inr ⟨Nat.le_trans h1 x, Nat.lt_of_lt_of_le y h2⟩

theorem local_range (ctx : Ctx) (lo hi : Nat) (l : List Occ) (hl : ∀ o ∈ l, o.scope = ctx.scope) :
    InRange ctx lo hi l := fun o ho => Or.inl (hl o ho)

theorem expr_scope (ctx : Ctx) : ∀ (e : Expr) (cs : CS), ∀ o ∈ (compileExpr cand ctx e cs).1, o.scope = ctx.scope
  | .attr l, cs, o, ho => by
    simp only [compileExpr, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl | rfl <;> rfl
  | .lit, cs, o, ho => by simp [compileExpr] at ho
  | .bin a b, cs, o, ho => by
    simp only [compileExpr, List.mem_append] at ho
    rcases ho with h | h
    · exact expr_scope ctx a cs o h
    · exact expr_scope ctx b _ o h

/-- Every occurrence is in the current scope, or in a scope that this call opened. -/
theorem compile_range : ∀ (ctx : Ctx) (f : F) (cs : CS),
    InRange ctx cs.next (compile cand ctx f cs).2.next (compile cand ctx f cs).1
  | ctx, .cmp l, cs => local_range ctx _ _ _ (by
      intro o ho; simp only [compile, List.mem_cons, List.not_mem_nil, or_false] at ho
      rcases ho with rfl | rfl | rfl <;> rfl)
  | ctx, .computed e, cs => by
    apply local_range
    intro o ho
    simp only [compile, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with h | rfl | rfl
    · exact expr_scope cand ctx e cs o h
    · rfl
    · rfl
  | ctx, .role rl f, cs => by
    simp only [compile]
    apply InRange.append
    · apply local_range; intro o ho
      simp only [List.mem_cons, List.not_mem_nil, or_false] at ho
      rcases ho with rfl | rfl <;> rfl
    · exact (compile_range { ctx with owner := _, ownerFam := .player } f _).widen
        (by rw [nm_next]; exact Nat.le_refl _) (Nat.le_refl _)
  | ctx, .and a b, cs => by
    simp only [compile]
    exact ((compile_range ctx a cs).widen (Nat.le_refl _) (compile_grows cand ctx b _).next).append
      ((compile_range ctx b _).widen (compile_grows cand ctx a cs).next (Nat.le_refl _))
  | ctx, .or a b, cs => by
    have ra := compile_range (childCtx ctx cs.next) a { cs with next := cs.next + 2 }
    have g1 := compile_grows cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }
    simp only [compile]
    generalize compile cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 } = r1 at ra g1 ⊢
    have rb := compile_range (childCtx ctx (cs.next + 1)) b r1.2
    have g2 := compile_grows cand (childCtx ctx (cs.next + 1)) b r1.2
    generalize compile cand (childCtx ctx (cs.next + 1)) b r1.2 = r2 at rb g2 ⊢
    have n1 := g1.next
    have n2 := g2.next
    simp only at n1
    apply InRange.append
    · intro o ho
      rcases ra o ho with e | ⟨x, y⟩
      · simp only [childCtx] at e; right; exact ⟨by omega, by omega⟩
      · simp only at x; right; exact ⟨by omega, by omega⟩
    · intro o ho
      rcases rb o ho with e | ⟨x, y⟩
      · simp only [childCtx] at e; right; exact ⟨by omega, by omega⟩
      · right; exact ⟨by omega, by omega⟩
  | ctx, .not a, cs => by
    have ra := compile_range (childCtx ctx cs.next) a { cs with next := cs.next + 1 }
    have g1 := compile_grows cand (childCtx ctx cs.next) a { cs with next := cs.next + 1 }
    simp only [compile]
    generalize compile cand (childCtx ctx cs.next) a { cs with next := cs.next + 1 } = r1 at ra g1 ⊢
    have n1 := g1.next
    simp only at n1
    intro o ho
    rcases ra o ho with e | ⟨x, y⟩
    · simp only [childCtx] at e; right; exact ⟨by omega, by omega⟩
    · simp only at x; right; exact ⟨by omega, by omega⟩

/-! ## Owners are bound in an enclosing scope -/

theorem expr_owner (ctx : Ctx) : ∀ (e : Expr) (cs : CS),
    ∀ o ∈ (compileExpr cand ctx e cs).1, o.kind = .ownerUse → o.n = ctx.owner
  | .attr l, cs, o, ho, hk => by
    simp only [compileExpr, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl | rfl
    · rfl
    · simp [occ] at hk
    · simp [occ] at hk
  | .lit, cs, o, ho, _ => by simp [compileExpr] at ho
  | .bin a b, cs, o, ho, hk => by
    simp only [compileExpr, List.mem_append] at ho
    rcases ho with h | h
    · exact expr_owner ctx a cs o h hk
    · exact expr_owner ctx b _ o h hk

def OwnersOK (ctx : Ctx) (out : List Occ) : Prop :=
  ∀ o ∈ out, o.kind = .ownerUse →
    o.n = ctx.owner ∨ ∃ b ∈ out, b.kind = .bind ∧ b.fam = .player ∧ b.n = o.n ∧ b.scope ∈ o.path

theorem OwnersOK.append {ctx : Ctx} {a b : List Occ} (ha : OwnersOK ctx a) (hb : OwnersOK ctx b) :
    OwnersOK ctx (a ++ b) := by
  intro o ho hk
  rcases List.mem_append.mp ho with h | h
  · rcases ha o h hk with e | ⟨x, hx, hh⟩
    · exact Or.inl e
    · exact Or.inr ⟨x, List.mem_append_left _ hx, hh⟩
  · rcases hb o h hk with e | ⟨x, hx, hh⟩
    · exact Or.inl e
    · exact Or.inr ⟨x, List.mem_append_right _ hx, hh⟩

theorem compile_owners : ∀ (ctx : Ctx) (f : F) (cs : CS), WF ctx → OwnersOK ctx (compile cand ctx f cs).1
  | ctx, .cmp l, cs, _ => by
    intro o ho hk
    simp only [compile, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl | rfl
    · exact Or.inl rfl
    · simp [occ] at hk
    · simp [occ] at hk
  | ctx, .computed e, cs, _ => by
    intro o ho hk
    simp only [compile, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with h | rfl | rfl
    · exact Or.inl (expr_owner cand ctx e cs o h hk)
    · simp [occ] at hk
    · simp [occ] at hk
  | ctx, .role rl f, cs, hw => by
    intro o ho hk
    simp only [compile, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with (rfl | rfl) | h
    · exact Or.inl rfl
    · simp [occ] at hk
    · let ictx : Ctx :=
        { ctx with owner := (nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩).1, ownerFam := .player }
      have hwi : WF ictx := hw
      rcases compile_owners ictx f _ hwi o h hk with e | ⟨x, hx, hh⟩
      · right
        refine ⟨occ ctx (nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩).1 .player .bind,
          by simp [compile], rfl, rfl, e.symm, ?_⟩
        obtain ⟨⟨t, ht⟩, _⟩ := compile_path cand ictx f _ hwi o h
        rw [ht]
        apply List.mem_append_right
        have : ctx.path.head? = some ctx.scope := hw
        cases hp : ctx.path with
        | nil => rw [hp] at this; simp at this
        | cons y ys => rw [hp] at this; simp at this; subst this; simp [occ]
      · exact Or.inr ⟨x, List.mem_append_right _ hx, hh⟩
  | ctx, .and a b, cs, hw => by
    simp only [compile]; exact (compile_owners ctx a cs hw).append (compile_owners ctx b _ hw)
  | ctx, .or a b, cs, hw => by
    simp only [compile]
    exact (compile_owners (childCtx ctx cs.next) a _ (by simp [WF, childCtx])).append
      (compile_owners (childCtx ctx (cs.next + 1)) b _ (by simp [WF, childCtx]))
  | ctx, .not a, cs, hw => by
    simp only [compile]
    exact compile_owners (childCtx ctx cs.next) a _ (by simp [WF, childCtx])

/-! ## Theorems for every query -/

def rootKey : Key := ⟨.root, [], [], 0⟩
def cs0 : CS := ⟨⟨[]⟩, 1⟩
def rootCtx : Ctx := ⟨(nm cand cs0 rootKey).1, .root, 0, [0]⟩

theorem query_eq (f : F) : compileQuery cand f =
    (occ (rootCtx cand) (nm cand cs0 rootKey).1 .root .bind ::
      (compile cand (rootCtx cand) f (nm cand cs0 rootKey).2).1,
     (compile cand (rootCtx cand) f (nm cand cs0 rootKey).2).2) := rfl

theorem query_backed (f : F) :
    ∀ o ∈ (compileQuery cand f).1, Backed (compileQuery cand f).2.tbl.table o := by
  rw [query_eq]
  have hob : OwnerBacked (nm cand cs0 rootKey).2.tbl.table (rootCtx cand) :=
    ⟨rootKey, nm_mem cand cs0 rootKey, rfl⟩
  intro o ho
  simp only [List.mem_cons] at ho
  rcases ho with rfl | h
  · exact Backed.mono (compile_grows cand _ f _) ⟨rootKey, nm_mem cand cs0 rootKey, rfl, fun _ => rfl⟩
  · exact compile_backed cand _ f _ hob o h

theorem query_inv (f : F) : Inv (compileQuery cand f).2.tbl := by
  rw [query_eq]
  exact (compile_grows cand _ f _).inv (nm_inv cand cs0 rootKey inv_start)

/-- Two occurrences with the same name belong to the same family. A player
never gets the name of its owner (fault 1), and no attribute variable gets
the name of a result, a player, or the root. -/
theorem same_name_same_family (f : F) :
    ∀ o₁ ∈ (compileQuery cand f).1, ∀ o₂ ∈ (compileQuery cand f).1, o₁.n = o₂.n → o₁.fam = o₂.fam := by
  intro o₁ h₁ o₂ h₂ he
  obtain ⟨k₁, m₁, f₁, _⟩ := query_backed cand f o₁ h₁
  obtain ⟨k₂, m₂, f₂, _⟩ := query_backed cand f o₂ h₂
  rw [he] at m₁
  have := inv_distinct _ (query_inv cand f) k₁ k₂ _ m₁ m₂
  rw [← f₁, ← f₂, this]

/-- R2 (locality): a variable that is not an owner use occurs in exactly one scope. -/
theorem locality (f : F) :
    ∀ o₁ ∈ (compileQuery cand f).1, ∀ o₂ ∈ (compileQuery cand f).1, o₁.n = o₂.n →
      o₁.kind ≠ .ownerUse → o₂.kind ≠ .ownerUse → o₁.scope = o₂.scope := by
  intro o₁ h₁ o₂ h₂ he n₁ n₂
  obtain ⟨k₁, m₁, _, s₁⟩ := query_backed cand f o₁ h₁
  obtain ⟨k₂, m₂, _, s₂⟩ := query_backed cand f o₂ h₂
  rw [he] at m₁
  have := inv_distinct _ (query_inv cand f) k₁ k₂ _ m₁ m₂
  rw [← s₁ n₁, ← s₂ n₂, this]

/-- Every use is bound in its own scope. No expression reads an unbound
attribute (fault 5), and no branch reads a renamed outer variable (fault 4). -/
theorem uses_bound (f : F) : UsesBound (compileQuery cand f).1 := by
  rw [query_eq]
  intro o ho hk
  simp only [List.mem_cons] at ho
  rcases ho with rfl | h
  · simp [occ] at hk
  · obtain ⟨b, hb, hh⟩ := compile_uses_bound cand _ f _ o h hk
    exact ⟨b, List.mem_cons_of_mem _ hb, hh⟩

/-- Every owner use reads a variable that is bound in the same scope or in an
enclosing scope (the root, or the player of an enclosing `RolePlayer`). -/
theorem owners_bound (f : F) :
    ∀ o ∈ (compileQuery cand f).1, o.kind = .ownerUse →
      ∃ b ∈ (compileQuery cand f).1, b.kind = .bind ∧ b.n = o.n ∧ b.scope ∈ o.path := by
  rw [query_eq]
  have hw : WF (rootCtx cand) := rfl
  intro o ho hk
  simp only [List.mem_cons] at ho
  rcases ho with rfl | h
  · simp [occ] at hk
  · rcases compile_owners cand _ f _ hw o h hk with e | ⟨b, hb, hk', _, hn, hs⟩
    · refine ⟨_, List.mem_cons_self, rfl, e.symm, ?_⟩
      obtain ⟨⟨t, ht⟩, _⟩ := compile_path cand _ f _ hw o h
      rw [ht]; simp [occ, rootCtx]
    · exact ⟨b, List.mem_cons_of_mem _ hb, hk', hn, hs⟩

/-- R2 (siblings): the two branches of an `or` never share a variable that is
not an owner use. -/
theorem or_branches_disjoint (ctx : Ctx) (a b : F) (cs : CS) (hi : Inv cs.tbl)
    (hob : OwnerBacked cs.tbl.table ctx) :
    ∀ x ∈ (compile cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }).1,
    ∀ y ∈ (compile cand (childCtx ctx (cs.next + 1)) b
        (compile cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }).2).1,
    x.kind ≠ .ownerUse → y.kind ≠ .ownerUse → x.n ≠ y.n := by
  intro x hx y hy nx ny he
  have g1 := compile_grows cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }
  have bx0 := compile_backed cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 } hob x hx
  have ra := compile_range cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 } x hx
  have hob1 := (show OwnerBacked ({ cs with next := cs.next + 2 } : CS).tbl.table
    (childCtx ctx (cs.next + 1)) from hob).mono g1
  generalize compile cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 } = r1 at *
  have g2 := compile_grows cand (childCtx ctx (cs.next + 1)) b r1.2
  have by' := compile_backed cand (childCtx ctx (cs.next + 1)) b r1.2 hob1 y hy
  have rb := compile_range cand (childCtx ctx (cs.next + 1)) b r1.2 y hy
  have hinv : Inv (compile cand (childCtx ctx (cs.next + 1)) b r1.2).2.tbl := g2.inv (g1.inv hi)
  have bx := bx0.mono g2
  generalize compile cand (childCtx ctx (cs.next + 1)) b r1.2 = r2 at *
  obtain ⟨kx, mx, _, sx⟩ := bx
  obtain ⟨ky, my, _, sy⟩ := by'
  rw [he] at mx
  have hk := inv_distinct _ hinv kx ky _ mx my
  have ex := sx nx
  have ey := sy ny
  rw [hk] at ex
  have n1 := g1.next
  simp only at n1 ra
  simp only [childCtx] at ra rb
  omega

/-- R2 (child scopes are fresh): every occurrence that a `not` body or an `or`
branch emits is in a scope other than the enclosing scope. With `locality`,
a child scope therefore never shares a variable (other than an owner) with
its enclosing scope. The precondition holds for every call: the root scope is
0 and the counter starts at 1, and each child scope is below the counter. -/
theorem not_body_fresh (ctx : Ctx) (a : F) (cs : CS) (h : ctx.scope < cs.next) :
    ∀ o ∈ (compile cand ctx (.not a) cs).1, o.scope ≠ ctx.scope := by
  intro o ho
  have r := compile_range cand (childCtx ctx cs.next) a { cs with next := cs.next + 1 } o ho
  simp only [childCtx] at r
  omega

theorem or_branch_fresh (ctx : Ctx) (a b : F) (cs : CS) (h : ctx.scope < cs.next) :
    ∀ o ∈ (compile cand ctx (.or a b) cs).1, o.scope ≠ ctx.scope := by
  intro o ho
  have r := compile_range cand ctx (.or a b) cs o ho
  simp only [compile, List.mem_append] at ho
  rcases ho with ho | ho
  · have ra := compile_range cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 } o ho
    simp only [childCtx] at ra
    omega
  · have rb := compile_range cand (childCtx ctx (cs.next + 1)) b
      (compile cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }).2 o ho
    have g1 := (compile_grows cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }).next
    simp only [childCtx] at g1 rb
    omega

/-! ## Computed results are unique

Each `Computed` filter takes a fresh id from the query counter, so no two
`Computed` filters share a result variable. -/

def isResBind (o : Occ) : Bool := o.fam == .result && o.kind == .bind

def resNames (out : List Occ) : List Name := (out.filter isResBind).map (·.n)

def resKey (id s : Nat) : Key := ⟨.result, [], digits id, s⟩

/-- The result binds of a call have distinct names, and each comes from a
result key whose id this call took from the counter. -/
def ResultsOK (cs cs' : CS) (out : List Occ) : Prop :=
  (resNames out).Nodup ∧
  ∀ n ∈ resNames out, ∃ id s, cs.next ≤ id ∧ id < cs'.next ∧ (resKey id s, n) ∈ cs'.tbl.table

theorem resNames_append (a b : List Occ) : resNames (a ++ b) = resNames a ++ resNames b := by
  simp [resNames, List.filter_append]

theorem expr_no_res (ctx : Ctx) : ∀ (e : Expr) (cs : CS), resNames (compileExpr cand ctx e cs).1 = []
  | .attr l, cs => by simp [compileExpr, resNames, isResBind, ownerOcc, occ]
  | .lit, cs => by simp [compileExpr, resNames]
  | .bin a b, cs => by
    simp only [compileExpr, resNames_append, expr_no_res ctx a, expr_no_res ctx b, List.append_nil]

theorem expr_next (ctx : Ctx) : ∀ (e : Expr) (cs : CS), (compileExpr cand ctx e cs).2.next = cs.next
  | .attr _, cs => rfl
  | .lit, cs => rfl
  | .bin a b, cs => by simp only [compileExpr]; rw [expr_next ctx b, expr_next ctx a]

/-- Joining two parts whose result ids come from disjoint counter ranges. -/
theorem ResultsOK.append {a m b : CS} {x y : List Occ} (hx : ResultsOK a m x) (hy : ResultsOK m b y)
    (ga : a.next ≤ m.next) (g : Grows m b) (hi : Inv b.tbl) : ResultsOK a b (x ++ y) := by
  refine ⟨?_, ?_⟩
  · rw [resNames_append, List.nodup_append]
    refine ⟨hx.1, hy.1, ?_⟩
    intro n₁ h₁ n₂ h₂ he
    obtain ⟨i₁, s₁, _, l₁, m₁⟩ := hx.2 n₁ h₁
    obtain ⟨i₂, s₂, u₂, _, m₂⟩ := hy.2 n₂ h₂
    rw [he] at m₁
    have hk := inv_distinct _ hi _ _ _ (g.mem m₁) m₂
    simp only [resKey, Key.mk.injEq, true_and] at hk
    have := digits_inj hk.1
    omega
  · intro n hn
    rw [resNames_append] at hn
    rcases List.mem_append.mp hn with h | h
    · obtain ⟨i, s, u, l, m⟩ := hx.2 n h
      exact ⟨i, s, u, Nat.lt_of_lt_of_le l g.next, g.mem m⟩
    · obtain ⟨i, s, u, l, m⟩ := hy.2 n h
      exact ⟨i, s, Nat.le_trans ga u, l, m⟩

theorem ResultsOK.weaken {a a' b : CS} {x : List Occ} (h : ResultsOK a b x) (hl : a'.next ≤ a.next) :
    ResultsOK a' b x := by
  refine ⟨h.1, fun n hn => ?_⟩
  obtain ⟨i, s, u, l, m⟩ := h.2 n hn
  exact ⟨i, s, Nat.le_trans hl u, l, m⟩

theorem results_nil (cs cs' : CS) (out : List Occ) (h : resNames out = []) : ResultsOK cs cs' out := by
  refine ⟨by rw [h]; exact List.nodup_nil, fun n hn => ?_⟩
  rw [h] at hn; simp at hn

theorem compile_results : ∀ (ctx : Ctx) (f : F) (cs : CS), Inv cs.tbl →
    ResultsOK cs (compile cand ctx f cs).2 (compile cand ctx f cs).1
  | ctx, .cmp l, cs, _ => results_nil _ _ _ (by simp [compile, resNames, isResBind, ownerOcc, occ])
  | ctx, .computed e, cs, _ => by
    simp only [compile]
    have hn := expr_next cand ctx e cs
    refine ⟨?_, ?_⟩
    · rw [resNames_append, expr_no_res]; simp [resNames, isResBind, occ]
    · intro n hn'
      rw [resNames_append, expr_no_res] at hn'
      simp [resNames, isResBind, occ] at hn'
      subst hn'
      refine ⟨(compileExpr cand ctx e cs).2.next, ctx.scope, by omega, ?_, nm_mem cand _ _⟩
      rw [nm_next]; simp
  | ctx, .role rl f, cs, hi => by
    simp only [compile]
    have ih := compile_results
      { ctx with owner := (nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩).1, ownerFam := .player }
      f (nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩).2 (nm_inv cand cs _ hi)
    have hpre : resNames ([ownerOcc ctx, occ ctx (nm cand cs ⟨.player, ctx.owner, rl, ctx.scope⟩).1 .player .bind]) = [] := by
      simp [resNames, isResBind, ownerOcc, occ]
    refine ⟨?_, ?_⟩
    · rw [resNames_append, hpre, List.nil_append]; exact ih.1
    · intro n hn
      rw [resNames_append, hpre, List.nil_append] at hn
      obtain ⟨i, s, u, l, m⟩ := ih.2 n hn
      exact ⟨i, s, by rw [nm_next] at u; exact u, l, m⟩
  | ctx, .and a b, cs, hi => by
    simp only [compile]
    have g1 := compile_grows cand ctx a cs
    have g2 := compile_grows cand ctx b (compile cand ctx a cs).2
    exact (compile_results ctx a cs hi).append (compile_results ctx b _ (g1.inv hi))
      g1.next g2 (g2.inv (g1.inv hi))
  | ctx, .or a b, cs, hi => by
    simp only [compile]
    have g0 := bump_grows cs (cs.next + 2) (by omega)
    have g1 := compile_grows cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }
    have g2 := compile_grows cand (childCtx ctx (cs.next + 1)) b
      (compile cand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }).2
    have ra := (compile_results (childCtx ctx cs.next) a { cs with next := cs.next + 2 } hi).weaken
      (a' := cs) (by simp)
    have rb := compile_results (childCtx ctx (cs.next + 1)) b _ (g1.inv hi)
    exact ra.append rb (Nat.le_trans g0.next g1.next) g2 (g2.inv (g1.inv hi))
  | ctx, .not a, cs, hi => by
    simp only [compile]
    exact (compile_results (childCtx ctx cs.next) a { cs with next := cs.next + 1 } hi).weaken
      (a' := cs) (by simp)

/-! ## Query builders

After the filters, a builder adds its own variables in the query scope:
sort attributes and the attributes of reduce specs (keyed like filter
attributes, so they share a variable with a filter on the same attribute,
R3), reduce outputs, and old values of a bulk update. The family of the
reduce outputs is a parameter: `build` gives them their own family, and
`buildShared` puts them in the `result` family of `Computed` results. -/

structure Spec where
  filter : F
  sorts : List Name
  reduces : List Name
  olds : Nat

def attrStage (ctx : Ctx) : List Name → CS → List Occ × CS
  | [], cs => ([], cs)
  | l :: ls, cs =>
    let r := nm cand cs (attrKey ctx l)
    let r2 := attrStage ctx ls r.2
    ([ownerOcc ctx, occ ctx r.1 .attr .bind, occ ctx r.1 .attr .use] ++ r2.1, r2.2)

def outKey (fam : Fam) (i s : Nat) : Key := ⟨fam, [], digits i, s⟩

def outStage (ctx : Ctx) (fam : Fam) : Nat → Nat → CS → List Occ × CS
  | _, 0, cs => ([], cs)
  | i, k + 1, cs =>
    let r := nm cand cs (outKey fam i ctx.scope)
    let r2 := outStage ctx fam (i + 1) k r.2
    ([occ ctx r.1 fam .bind, occ ctx r.1 fam .use] ++ r2.1, r2.2)

def buildWith (redFam : Fam) (q : Spec) : List Occ × CS :=
  let r0 := compileQuery cand q.filter
  let r1 := attrStage cand (rootCtx cand) (q.sorts ++ q.reduces) r0.2
  let r2 := outStage cand (rootCtx cand) redFam 0 q.reduces.length r1.2
  let r3 := outStage cand (rootCtx cand) .old 0 q.olds r2.2
  (r0.1 ++ r1.1 ++ r2.1 ++ r3.1, r3.2)

/-- The design with its own family for reduce outputs. -/
def build (q : Spec) : List Occ × CS := buildWith cand .reduce q

/-- The keying that the design left open: reduce outputs in the `result` family. -/
def buildShared (q : Spec) : List Occ × CS := buildWith cand .result q

theorem attrStage_grows (ctx : Ctx) : ∀ (ls : List Name) (cs : CS), Grows cs (attrStage cand ctx ls cs).2
  | [], cs => Grows.refl cs
  | _ :: ls, cs => (nm_grows cand cs _).trans (attrStage_grows ctx ls _)

theorem outStage_grows (ctx : Ctx) (fam : Fam) : ∀ (i k : Nat) (cs : CS),
    Grows cs (outStage cand ctx fam i k cs).2
  | _, 0, cs => Grows.refl cs
  | i, k + 1, cs => (nm_grows cand cs _).trans (outStage_grows ctx fam (i + 1) k _)

theorem attrStage_backed (ctx : Ctx) : ∀ (ls : List Name) (cs : CS), OwnerBacked cs.tbl.table ctx →
    ∀ o ∈ (attrStage cand ctx ls cs).1, Backed (attrStage cand ctx ls cs).2.tbl.table o
  | [], cs, _, o, ho => by simp [attrStage] at ho
  | l :: ls, cs, hob, o, ho => by
    simp only [attrStage, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    have g := attrStage_grows cand ctx ls (nm cand cs (attrKey ctx l)).2
    rcases ho with (rfl | rfl | rfl) | h
    · exact (ownerOcc_backed _ ctx (hob.mono (nm_grows cand cs _))).mono g
    · exact (attr_backed cand cs ctx l _).mono g
    · exact (attr_backed cand cs ctx l _).mono g
    · exact attrStage_backed ctx ls _ (hob.mono (nm_grows cand cs _)) o h

theorem outStage_backed (ctx : Ctx) (fam : Fam) : ∀ (i k : Nat) (cs : CS),
    ∀ o ∈ (outStage cand ctx fam i k cs).1, Backed (outStage cand ctx fam i k cs).2.tbl.table o
  | _, 0, cs, o, ho => by simp [outStage] at ho
  | i, k + 1, cs, o, ho => by
    simp only [outStage, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    have g := outStage_grows cand ctx fam (i + 1) k (nm cand cs (outKey fam i ctx.scope)).2
    rcases ho with (rfl | rfl) | h
    · exact Backed.mono g ⟨_, nm_mem cand _ _, rfl, fun _ => rfl⟩
    · exact Backed.mono g ⟨_, nm_mem cand _ _, rfl, fun _ => rfl⟩
    · exact outStage_backed ctx fam (i + 1) k _ o h

def famBindNames (fam : Fam) (out : List Occ) : List Name :=
  (out.filter (fun o => o.fam == fam && o.kind == .bind)).map (·.n)

theorem famBindNames_append (fam : Fam) (a b : List Occ) :
    famBindNames fam (a ++ b) = famBindNames fam a ++ famBindNames fam b := by
  simp [famBindNames, List.filter_append]

/-- The outputs of one stage have distinct names: output `i` uses key `i`. -/
theorem outStage_nodup (ctx : Ctx) (fam : Fam) : ∀ (i k : Nat) (cs : CS), Inv cs.tbl →
    (famBindNames fam (outStage cand ctx fam i k cs).1).Nodup ∧
    ∀ n ∈ famBindNames fam (outStage cand ctx fam i k cs).1, ∃ j, i ≤ j ∧ j < i + k ∧
      (outKey fam j ctx.scope, n) ∈ (outStage cand ctx fam i k cs).2.tbl.table
  | _, 0, cs, _ => by simp [outStage, famBindNames]
  | i, k + 1, cs, hi => by
    have ih := outStage_nodup ctx fam (i + 1) k (nm cand cs (outKey fam i ctx.scope)).2 (nm_inv cand cs _ hi)
    have g := outStage_grows cand ctx fam (i + 1) k (nm cand cs (outKey fam i ctx.scope)).2
    have hinv := g.inv (nm_inv cand cs _ hi)
    have hm := g.mem (nm_mem cand cs (outKey fam i ctx.scope))
    simp only [outStage, famBindNames_append]
    have hhead : famBindNames fam [occ ctx (nm cand cs (outKey fam i ctx.scope)).1 fam .bind,
        occ ctx (nm cand cs (outKey fam i ctx.scope)).1 fam .use] = [(nm cand cs (outKey fam i ctx.scope)).1] := by
      simp [famBindNames, occ]
    rw [hhead]
    refine ⟨?_, ?_⟩
    · apply List.nodup_cons.mpr
      refine ⟨fun hmem => ?_, ih.1⟩
      obtain ⟨j, hj, _, mj⟩ := ih.2 _ hmem
      have hk := inv_distinct _ hinv _ _ _ hm mj
      simp only [outKey, Key.mk.injEq, true_and] at hk
      have := digits_inj hk.1
      omega
    · intro n hn
      simp only [List.cons_append, List.nil_append, List.mem_cons] at hn
      rcases hn with rfl | hn
      · exact ⟨i, Nat.le_refl _, by omega, hm⟩
      · obtain ⟨j, h1, h2, mj⟩ := ih.2 n hn
        exact ⟨j, by omega, by omega, mj⟩

/-! ### Theorems for every built query -/

theorem expr_bind_fams (ctx : Ctx) : ∀ (e : Expr) (cs : CS),
    ∀ o ∈ (compileExpr cand ctx e cs).1, o.kind = .bind → o.fam = .attr
  | .attr l, cs, o, ho, hk => by
    simp only [compileExpr, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl | rfl
    · simp [ownerOcc] at hk
    · rfl
    · simp [occ] at hk
  | .lit, cs, o, ho, _ => by simp [compileExpr] at ho
  | .bin a b, cs, o, ho, hk => by
    simp only [compileExpr, List.mem_append] at ho
    rcases ho with h | h
    · exact expr_bind_fams ctx a cs o h hk
    · exact expr_bind_fams ctx b _ o h hk

/-- Filter compilation binds only attributes, players, and `Computed` results. -/
theorem compile_bind_fams : ∀ (ctx : Ctx) (f : F) (cs : CS),
    ∀ o ∈ (compile cand ctx f cs).1, o.kind = .bind → o.fam = .attr ∨ o.fam = .player ∨ o.fam = .result
  | ctx, .cmp l, cs, o, ho, hk => by
    simp only [compile, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl | rfl
    · simp [ownerOcc] at hk
    · exact Or.inl rfl
    · simp [occ] at hk
  | ctx, .computed e, cs, o, ho, hk => by
    simp only [compile, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with h | rfl | rfl
    · exact Or.inl (expr_bind_fams cand ctx e cs o h hk)
    · exact Or.inr (Or.inr rfl)
    · simp [occ] at hk
  | ctx, .role rl f, cs, o, ho, hk => by
    simp only [compile, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with (rfl | rfl) | h
    · simp [ownerOcc] at hk
    · exact Or.inr (Or.inl rfl)
    · exact compile_bind_fams _ f _ o h hk
  | ctx, .and a b, cs, o, ho, hk => by
    simp only [compile, List.mem_append] at ho
    rcases ho with h | h
    · exact compile_bind_fams ctx a cs o h hk
    · exact compile_bind_fams ctx b _ o h hk
  | ctx, .or a b, cs, o, ho, hk => by
    simp only [compile, List.mem_append] at ho
    rcases ho with h | h
    · exact compile_bind_fams _ a _ o h hk
    · exact compile_bind_fams _ b _ o h hk
  | ctx, .not a, cs, o, ho, hk => by
    simp only [compile] at ho
    exact compile_bind_fams _ a _ o ho hk

theorem famBindNames_nil (fam : Fam) (out : List Occ)
    (h : ∀ o ∈ out, o.kind = .bind → o.fam ≠ fam) : famBindNames fam out = [] := by
  simp only [famBindNames, List.map_eq_nil_iff, List.filter_eq_nil_iff, Bool.and_eq_true, beq_iff_eq,
    not_and]
  intro o ho hf hk
  exact h o ho hk hf

theorem attrStage_bind_fams (ctx : Ctx) : ∀ (ls : List Name) (cs : CS),
    ∀ o ∈ (attrStage cand ctx ls cs).1, o.kind = .bind → o.fam = .attr
  | [], cs, o, ho, _ => by simp [attrStage] at ho
  | l :: ls, cs, o, ho, hk => by
    simp only [attrStage, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with (rfl | rfl | rfl) | h
    · simp [ownerOcc] at hk
    · rfl
    · simp [occ] at hk
    · exact attrStage_bind_fams ctx ls _ o h hk

theorem outStage_fams (ctx : Ctx) (fam : Fam) : ∀ (i k : Nat) (cs : CS),
    ∀ o ∈ (outStage cand ctx fam i k cs).1, o.fam = fam
  | _, 0, cs, o, ho => by simp [outStage] at ho
  | i, k + 1, cs, o, ho => by
    simp only [outStage, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with (rfl | rfl) | h
    · rfl
    · rfl
    · exact outStage_fams ctx fam (i + 1) k _ o h

theorem query_owner_backed (f : F) :
    OwnerBacked (compileQuery cand f).2.tbl.table (rootCtx cand) := by
  rw [query_eq]
  exact OwnerBacked.mono (compile_grows cand _ f _) ⟨rootKey, nm_mem cand cs0 rootKey, rfl⟩

theorem build_backed (q : Spec) : ∀ o ∈ (build cand q).1, Backed (build cand q).2.tbl.table o := by
  simp only [build, buildWith]
  have g1 := attrStage_grows cand (rootCtx cand) (q.sorts ++ q.reduces) (compileQuery cand q.filter).2
  have b1 := attrStage_backed cand (rootCtx cand) (q.sorts ++ q.reduces) (compileQuery cand q.filter).2
    (query_owner_backed cand q.filter)
  generalize attrStage cand (rootCtx cand) (q.sorts ++ q.reduces) (compileQuery cand q.filter).2 = r1 at *
  have g2 := outStage_grows cand (rootCtx cand) .reduce 0 q.reduces.length r1.2
  have b2 := outStage_backed cand (rootCtx cand) .reduce 0 q.reduces.length r1.2
  generalize outStage cand (rootCtx cand) .reduce 0 q.reduces.length r1.2 = r2 at *
  have g3 := outStage_grows cand (rootCtx cand) .old 0 q.olds r2.2
  have b3 := outStage_backed cand (rootCtx cand) .old 0 q.olds r2.2
  generalize outStage cand (rootCtx cand) .old 0 q.olds r2.2 = r3 at *
  intro o ho
  simp only [List.mem_append] at ho
  rcases ho with ((h | h) | h) | h
  · exact (((query_backed cand q.filter o h).mono g1).mono g2).mono g3
  · exact ((b1 o h).mono g2).mono g3
  · exact (b2 o h).mono g3
  · exact b3 o h

theorem build_inv (q : Spec) : Inv (build cand q).2.tbl := by
  simp only [build, buildWith]
  exact (outStage_grows cand _ .old 0 q.olds _).inv ((outStage_grows cand _ .reduce 0 _ _).inv
    ((attrStage_grows cand _ _ _).inv (query_inv cand q.filter)))

/-- Names never cross families in a built query. -/
theorem build_same_name_same_family (q : Spec) :
    ∀ o₁ ∈ (build cand q).1, ∀ o₂ ∈ (build cand q).1, o₁.n = o₂.n → o₁.fam = o₂.fam := by
  intro o₁ h₁ o₂ h₂ he
  obtain ⟨k₁, m₁, f₁, _⟩ := build_backed cand q o₁ h₁
  obtain ⟨k₂, m₂, f₂, _⟩ := build_backed cand q o₂ h₂
  rw [he] at m₁
  have := inv_distinct _ (build_inv cand q) k₁ k₂ _ m₁ m₂
  rw [← f₁, ← f₂, this]

/-- Output uniqueness: every `Computed` result, reduce output, and old value is
bound once. With `build_same_name_same_family`, no two outputs share a variable. -/
theorem build_outputs_unique (q : Spec) :
    (famBindNames .result (build cand q).1).Nodup ∧
    (famBindNames .reduce (build cand q).1).Nodup ∧
    (famBindNames .old (build cand q).1).Nodup := by
  simp only [build, buildWith, famBindNames_append]
  have hq0 : Inv (nm cand cs0 rootKey).2.tbl := nm_inv cand cs0 rootKey inv_start
  have hres := compile_results cand (rootCtx cand) q.filter (nm cand cs0 rootKey).2 hq0
  have i1 := (attrStage_grows cand (rootCtx cand) (q.sorts ++ q.reduces) (compileQuery cand q.filter).2).inv
    (query_inv cand q.filter)
  have hb1 := attrStage_bind_fams cand (rootCtx cand) (q.sorts ++ q.reduces) (compileQuery cand q.filter).2
  generalize attrStage cand (rootCtx cand) (q.sorts ++ q.reduces) (compileQuery cand q.filter).2 = r1 at *
  have n2 := outStage_nodup cand (rootCtx cand) .reduce 0 q.reduces.length r1.2 i1
  have f2 := outStage_fams cand (rootCtx cand) .reduce 0 q.reduces.length r1.2
  have i2 := (outStage_grows cand (rootCtx cand) .reduce 0 q.reduces.length r1.2).inv i1
  generalize outStage cand (rootCtx cand) .reduce 0 q.reduces.length r1.2 = r2 at *
  have n3 := outStage_nodup cand (rootCtx cand) .old 0 q.olds r2.2 i2
  have f3 := outStage_fams cand (rootCtx cand) .old 0 q.olds r2.2
  generalize outStage cand (rootCtx cand) .old 0 q.olds r2.2 = r3 at *
  have c0 := compile_bind_fams cand (rootCtx cand) q.filter (nm cand cs0 rootKey).2
  rw [query_eq]
  have e1 : ∀ fam, fam ≠ .attr → famBindNames fam r1.1 = [] := fun fam hf =>
    famBindNames_nil fam _ (fun o ho hk e => hf (e ▸ hb1 o ho hk))
  have e2 : ∀ fam, fam ≠ .reduce → famBindNames fam r2.1 = [] := fun fam hf =>
    famBindNames_nil fam _ (fun o ho _ e => hf (e ▸ f2 o ho))
  have e3 : ∀ fam, fam ≠ .old → famBindNames fam r3.1 = [] := fun fam hf =>
    famBindNames_nil fam _ (fun o ho _ e => hf (e ▸ f3 o ho))
  have e0 : ∀ fam, fam ≠ .attr → fam ≠ .player → fam ≠ .result →
      famBindNames fam (compile cand (rootCtx cand) q.filter (nm cand cs0 rootKey).2).1 = [] :=
    fun fam ha hp hr => famBindNames_nil fam _ (fun o ho hk e => by
      rcases c0 o ho hk with h | h | h <;> (rw [h] at e; subst e; contradiction))
  have hroot : ∀ fam (l : List Occ), fam ≠ .root →
      famBindNames fam (occ (rootCtx cand) (nm cand cs0 rootKey).1 .root .bind :: l) = famBindNames fam l := by
    intro fam l hf
    simp [famBindNames, occ, Ne.symm hf]
  have hr : resNames (compile cand (rootCtx cand) q.filter (nm cand cs0 rootKey).2).1 =
      famBindNames .result (compile cand (rootCtx cand) q.filter (nm cand cs0 rootKey).2).1 := rfl
  refine ⟨?_, ?_, ?_⟩
  · rw [hroot _ _ (by decide), e1 _ (by decide), e2 _ (by decide), e3 _ (by decide), ← hr]
    simpa using hres.1
  · rw [hroot _ _ (by decide), e0 _ (by decide) (by decide) (by decide), e1 _ (by decide),
      e3 _ (by decide)]
    simpa using n2.1
  · rw [hroot _ _ (by decide), e0 _ (by decide) (by decide) (by decide), e1 _ (by decide),
      e2 _ (by decide)]
    simpa using n3.1

/-! ## The R2 example of the issue

`And(Gt("age", 1), Not(Eq("age", 5)))`: the `not` body binds its own `age`
variable. With a candidate that ignores the scope, the allocator gives it the
suffix `_2`. -/

def demoCand (k : Key) : Name :=
  match k.fam with
  | .root => bytes "e"
  | .attr => k.owner ++ [95, 95] ++ k.label
  | .player => k.label
  | .result => bytes "result" ++ k.label
  | .reduce => bytes "result" ++ k.label
  | .old => bytes "old" ++ k.label

example :
    ((compileQuery demoCand (.and (.cmp (bytes "age")) (.not (.cmp (bytes "age"))))).1.filter
      (fun o => o.fam == .attr && o.kind == .bind)).map (fun o => (o.n, o.scope)) =
    [(bytes "e__age", 0), (bytes "e__age_2", 1)] := by
  decide

/-! ### Concrete checks -/

def ageL : Name := bytes "age"

/-- R3: a sort on `age` shares the variable of a filter on `age`. -/
example :
    ((build demoCand ⟨.cmp ageL, [ageL], [], 0⟩).1.filter (fun o => o.fam == .attr && o.kind == .bind)).map
      (·.n) = [bytes "e__age", bytes "e__age"] := by
  decide

/-- The keying that the design left open merges a reduce output with a
`Computed` result: `Computed` takes counter id 1, and reduce spec 1 has index 1,
so both get the key `(result, [], "1", 0)` and the name `result1`. -/
example :
    famBindNames .result (buildShared demoCand ⟨.computed (.attr ageL), [], [ageL, ageL], 0⟩).1 =
      [bytes "result1", bytes "result0", bytes "result1"] := by
  decide

example :
    ¬ (famBindNames .result (buildShared demoCand ⟨.computed (.attr ageL), [], [ageL, ageL], 0⟩).1).Nodup := by
  decide

/-- With their own family, the reduce outputs get distinct keys. The candidate
`result1` is taken, so the table gives the suffix `_2`. -/
example :
    famBindNames .result (build demoCand ⟨.computed (.attr ageL), [], [ageL, ageL], 0⟩).1 = [bytes "result1"] ∧
    famBindNames .reduce (build demoCand ⟨.computed (.attr ageL), [], [ageL, ageL], 0⟩).1 =
      [bytes "result0", bytes "result1_2"] := by
  decide

/-! ## R8: the design candidates are valid

The candidates of the R8 table, as a function of the key. The key records the
scope id, not the kind of scope, so a child scope uses one prefix form,
`<owner>_s<i>`. -/

def scopedOwner (owner : Name) (s : Nat) : Name :=
  if s = 0 then owner else owner ++ VarLabel.us :: 115 :: digits s

def designCand (k : Key) : Name :=
  match k.fam with
  | .root => bytes "e"
  | .attr => attrName (scopedOwner k.owner k.scope) k.label
  | .player => rootSpelling k.label
  | .result => bytes "result" ++ k.label
  | .reduce => bytes "result" ++ k.label
  | .old => bytes "old" ++ k.label

/-- Every name in the table is a valid TypeQL variable. -/
def TableValid (t : List (Key × Name)) : Prop := ∀ p ∈ t, ValidVar p.2

/-- The labels of a filter tree are valid: attribute labels are bytes, and
role labels pass `identifierPattern` (R7). -/
def Bytes (l : Name) : Prop := ∀ x ∈ l, x < 256

def WFExpr : Expr → Prop
  | .attr l => Bytes l
  | .lit => True
  | .bin a b => WFExpr a ∧ WFExpr b

def WFTree : F → Prop
  | .cmp l => Bytes l
  | .computed e => WFExpr e
  | .role r f => ValidLabel r ∧ WFTree f
  | .and a b => WFTree a ∧ WFTree b
  | .or a b => WFTree a ∧ WFTree b
  | .not f => WFTree f

theorem scopedOwner_valid (o : Name) (s : Nat) (h : ValidVar o) : ValidVar (scopedOwner o s) := by
  unfold scopedOwner
  split
  · exact h
  · obtain ⟨⟨c, r, rfl, hc⟩, hall⟩ := h
    refine ⟨⟨c, r ++ VarLabel.us :: 115 :: digits s, by simp, hc⟩, ?_⟩
    intro x hx
    simp only [List.mem_append, List.mem_cons] at hx
    rcases hx with hx | rfl | rfl | hx
    · exact hall x (List.mem_cons.mpr hx)
    · decide
    · decide
    · simp [isVarChar, digits_alnum s x hx]

theorem prefix_digits_valid (p : Name) (hp : ValidVar p) (n : Nat) : ValidVar (p ++ digits n) := by
  obtain ⟨⟨c, r, rfl, hc⟩, hall⟩ := hp
  refine ⟨⟨c, r ++ digits n, by simp, hc⟩, ?_⟩
  intro x hx
  simp only [List.mem_append] at hx
  rcases hx with hx | hx
  · exact hall x hx
  · simp [isVarChar, digits_alnum n x hx]

theorem result_valid : ValidVar (bytes "result") := ⟨⟨114, bytes "esult", by decide, by decide⟩, by decide⟩
theorem old_valid : ValidVar (bytes "old") := ⟨⟨111, bytes "ld", by decide, by decide⟩, by decide⟩
theorem e_valid : ValidVar (bytes "e") := ⟨⟨101, [], by decide, by decide⟩, by decide⟩

theorem nm_valid (cs : CS) (k : Key) (ht : TableValid cs.tbl.table) (hk : ValidVar (designCand k)) :
    TableValid (nm designCand cs k).2.tbl.table := by
  unfold nm alloc
  cases hl : cs.tbl.table.lookup k with
  | some n => simpa [hl] using ht
  | none =>
    intro p hp
    simp only [List.mem_cons] at hp
    rcases hp with rfl | hp
    · exact pick_valid _ _ hk
    · exact ht p hp

theorem owner_valid (t : List (Key × Name)) (ctx : Ctx) (hob : OwnerBacked t ctx) (ht : TableValid t) :
    ValidVar ctx.owner := by
  obtain ⟨k, hk, _⟩ := hob
  exact ht _ hk

theorem attrKey_valid (ctx : Ctx) (l : Name) (ho : ValidVar ctx.owner) (hl : Bytes l) :
    ValidVar (designCand (attrKey ctx l)) :=
  attrName_valid _ _ (scopedOwner_valid _ _ ho) hl

theorem expr_valid (ctx : Ctx) : ∀ (e : Expr) (cs : CS), WFExpr e → ValidVar ctx.owner →
    TableValid cs.tbl.table → TableValid (compileExpr designCand ctx e cs).2.tbl.table
  | .attr l, cs, he, ho, ht => nm_valid cs _ ht (attrKey_valid ctx l ho he)
  | .lit, _, _, _, ht => ht
  | .bin a b, cs, he, ho, ht => expr_valid ctx b _ he.2 ho (expr_valid ctx a cs he.1 ho ht)

theorem compile_valid : ∀ (ctx : Ctx) (f : F) (cs : CS), WFTree f → OwnerBacked cs.tbl.table ctx →
    TableValid cs.tbl.table → TableValid (compile designCand ctx f cs).2.tbl.table
  | ctx, .cmp l, cs, hf, hob, ht => nm_valid cs _ ht (attrKey_valid ctx l (owner_valid _ ctx hob ht) hf)
  | ctx, .computed e, cs, hf, hob, ht => by
    simp only [compile]
    have h1 := expr_valid ctx e cs hf (owner_valid _ ctx hob ht) ht
    exact nm_valid _ _ h1 (prefix_digits_valid _ result_valid _)
  | ctx, .role rl f, cs, hf, hob, ht => by
    simp only [compile]
    have h1 := nm_valid cs ⟨.player, ctx.owner, rl, ctx.scope⟩ ht (rootSpelling_valid rl hf.1)
    exact compile_valid _ f _ hf.2 ⟨_, nm_mem designCand _ _, rfl⟩ h1
  | ctx, .and a b, cs, hf, hob, ht => by
    simp only [compile]
    have g1 := compile_grows designCand ctx a cs
    exact compile_valid ctx b _ hf.2 (hob.mono g1) (compile_valid ctx a cs hf.1 hob ht)
  | ctx, .or a b, cs, hf, hob, ht => by
    simp only [compile]
    have g1 := compile_grows designCand (childCtx ctx cs.next) a { cs with next := cs.next + 2 }
    have h1 := compile_valid (childCtx ctx cs.next) a { cs with next := cs.next + 2 } hf.1 hob ht
    exact compile_valid (childCtx ctx (cs.next + 1)) b _ hf.2
      ((show OwnerBacked ({ cs with next := cs.next + 2 } : CS).tbl.table (childCtx ctx (cs.next + 1))
        from hob).mono g1) h1
  | ctx, .not a, cs, hf, hob, ht => by
    simp only [compile]
    exact compile_valid (childCtx ctx cs.next) a { cs with next := cs.next + 1 } hf hob ht

/-- R8: with the design candidates, every variable of a query is a valid TypeQL
variable, for every filter tree with valid labels. -/
theorem query_names_valid (f : F) (hf : WFTree f) :
    ∀ o ∈ (compileQuery designCand f).1, ValidVar o.n := by
  have ht0 : TableValid (nm designCand cs0 rootKey).2.tbl.table :=
    nm_valid cs0 rootKey (by simp [TableValid, cs0]) e_valid
  have hob : OwnerBacked (nm designCand cs0 rootKey).2.tbl.table (rootCtx designCand) :=
    ⟨rootKey, nm_mem designCand cs0 rootKey, rfl⟩
  have ht := compile_valid (rootCtx designCand) f _ hf hob ht0
  intro o ho
  obtain ⟨k, hk, _⟩ := query_backed designCand f o ho
  rw [query_eq] at hk
  exact ht _ hk

theorem attrStage_valid (ctx : Ctx) : ∀ (ls : List Name) (cs : CS), (∀ l ∈ ls, Bytes l) →
    OwnerBacked cs.tbl.table ctx → TableValid cs.tbl.table →
    TableValid (attrStage designCand ctx ls cs).2.tbl.table
  | [], _, _, _, ht => ht
  | l :: ls, cs, hl, hob, ht => by
    simp only [attrStage]
    have h1 := nm_valid cs (attrKey ctx l) ht
      (attrKey_valid ctx l (owner_valid _ ctx hob ht) (hl l (by simp)))
    exact attrStage_valid ctx ls _ (fun x hx => hl x (List.mem_cons_of_mem _ hx))
      (hob.mono (nm_grows designCand cs _)) h1

theorem outStage_valid (ctx : Ctx) (fam : Fam) (hfam : fam = .reduce ∨ fam = .old) :
    ∀ (i k : Nat) (cs : CS), TableValid cs.tbl.table →
    TableValid (outStage designCand ctx fam i k cs).2.tbl.table
  | _, 0, _, ht => ht
  | i, k + 1, cs, ht => by
    simp only [outStage]
    have hk : ValidVar (designCand (outKey fam i ctx.scope)) := by
      rcases hfam with rfl | rfl
      · exact prefix_digits_valid _ result_valid i
      · exact prefix_digits_valid _ old_valid i
    exact outStage_valid ctx fam hfam (i + 1) k _ (nm_valid cs _ ht hk)

/-- R8 for built queries: every variable is a valid TypeQL variable, for every
filter tree and builder spec with valid labels. -/
theorem build_names_valid (q : Spec) (hf : WFTree q.filter) (hl : ∀ l ∈ q.sorts ++ q.reduces, Bytes l) :
    ∀ o ∈ (build designCand q).1, ValidVar o.n := by
  have ht0 : TableValid (nm designCand cs0 rootKey).2.tbl.table :=
    nm_valid cs0 rootKey (by simp [TableValid, cs0]) e_valid
  have hq : TableValid (compileQuery designCand q.filter).2.tbl.table := by
    rw [query_eq]
    exact compile_valid (rootCtx designCand) q.filter _ hf ⟨rootKey, nm_mem designCand cs0 rootKey, rfl⟩ ht0
  have h1 := attrStage_valid (rootCtx designCand) (q.sorts ++ q.reduces) _ hl
    (query_owner_backed designCand q.filter) hq
  have h2 := outStage_valid (rootCtx designCand) .reduce (Or.inl rfl) 0 q.reduces.length _ h1
  have h3 := outStage_valid (rootCtx designCand) .old (Or.inr rfl) 0 q.olds _ h2
  intro o ho
  obtain ⟨k, hk, _⟩ := build_backed designCand q o ho
  exact h3 _ hk

/-! ## R5: one binding per variable and scope

`build` emits a binding for each use of an attribute. The compiler of R5 keeps
the first binding of each (variable, scope) pair and drops the repeats. The
pass below does this. All properties of `build` hold for its result, and the
result has exactly one binding per variable and scope. -/

theorem attrStage_uses_bound (ctx : Ctx) : ∀ (ls : List Name) (cs : CS),
    UsesBound (attrStage cand ctx ls cs).1
  | [], cs => by intro o ho; simp [attrStage] at ho
  | l :: ls, cs => by
    simp only [attrStage]
    exact (attr_triple_bound ctx _).append (attrStage_uses_bound ctx ls _)

theorem outStage_uses_bound (ctx : Ctx) (fam : Fam) : ∀ (i k : Nat) (cs : CS),
    UsesBound (outStage cand ctx fam i k cs).1
  | _, 0, cs => by intro o ho; simp [outStage] at ho
  | i, k + 1, cs => by
    simp only [outStage]
    apply UsesBound.append _ (outStage_uses_bound ctx fam (i + 1) k _)
    intro o ho hk
    simp only [List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with rfl | rfl
    · simp [occ] at hk
    · exact ⟨occ ctx _ fam .bind, by simp, rfl, rfl, rfl⟩

theorem build_uses_bound (q : Spec) : UsesBound (build cand q).1 := by
  simp only [build, buildWith]
  exact (((uses_bound cand q.filter).append (attrStage_uses_bound cand _ _ _)).append
    (outStage_uses_bound cand _ _ _ _ _)).append (outStage_uses_bound cand _ _ _ _ _)

theorem attrStage_owner (ctx : Ctx) : ∀ (ls : List Name) (cs : CS),
    ∀ o ∈ (attrStage cand ctx ls cs).1, o.kind = .ownerUse → o.n = ctx.owner ∧ o.path = ctx.path
  | [], cs, o, ho, _ => by simp [attrStage] at ho
  | l :: ls, cs, o, ho, hk => by
    simp only [attrStage, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with (rfl | rfl | rfl) | h
    · exact ⟨rfl, rfl⟩
    · simp [occ] at hk
    · simp [occ] at hk
    · exact attrStage_owner ctx ls _ o h hk

theorem outStage_no_owner (ctx : Ctx) (fam : Fam) : ∀ (i k : Nat) (cs : CS),
    ∀ o ∈ (outStage cand ctx fam i k cs).1, o.kind ≠ .ownerUse
  | _, 0, cs, o, ho => by simp [outStage] at ho
  | i, k + 1, cs, o, ho => by
    simp only [outStage, List.mem_append, List.mem_cons, List.not_mem_nil, or_false] at ho
    rcases ho with (rfl | rfl) | h
    · simp [occ]
    · simp [occ]
    · exact outStage_no_owner ctx fam (i + 1) k _ o h

theorem build_owners_bound (q : Spec) :
    ∀ o ∈ (build cand q).1, o.kind = .ownerUse →
      ∃ b ∈ (build cand q).1, b.kind = .bind ∧ b.n = o.n ∧ b.scope ∈ o.path := by
  intro o ho hk
  have hsub : ∀ x ∈ (compileQuery cand q.filter).1, x ∈ (build cand q).1 := by
    intro x hx; simp only [build, buildWith, List.mem_append]; exact Or.inl (Or.inl (Or.inl hx))
  have hrootmem : occ (rootCtx cand) (nm cand cs0 rootKey).1 .root .bind ∈ (compileQuery cand q.filter).1 := by
    rw [query_eq]; exact List.mem_cons_self
  simp only [build, buildWith, List.mem_append] at ho
  rcases ho with ((h | h) | h) | h
  · obtain ⟨b, hb, hh⟩ := owners_bound cand q.filter o h hk
    exact ⟨b, hsub b hb, hh⟩
  · obtain ⟨hn, hp⟩ := attrStage_owner cand (rootCtx cand) _ _ o h hk
    refine ⟨_, hsub _ hrootmem, rfl, hn.symm, ?_⟩
    rw [hp]; simp [occ, rootCtx]
  · exact absurd hk (outStage_no_owner cand _ _ _ _ _ o h)
  · exact absurd hk (outStage_no_owner cand _ _ _ _ _ o h)

/-- Keep the first binding of each (variable, scope) pair. -/
def dedupAux : List (Name × Nat) → List Occ → List Occ
  | _, [] => []
  | seen, o :: os =>
    if o.kind = .bind then
      if (o.n, o.scope) ∈ seen then dedupAux seen os
      else o :: dedupAux ((o.n, o.scope) :: seen) os
    else o :: dedupAux seen os

def dedup (out : List Occ) : List Occ := dedupAux [] out

def bindPairs (out : List Occ) : List (Name × Nat) :=
  (out.filter (fun o => o.kind == .bind)).map (fun o => (o.n, o.scope))

theorem dedupAux_sublist : ∀ (seen : List (Name × Nat)) (l : List Occ), (dedupAux seen l).Sublist l
  | _, [] => List.Sublist.slnil
  | seen, o :: os => by
    unfold dedupAux
    split
    · split
      · exact (dedupAux_sublist seen os).cons o
      · exact (dedupAux_sublist _ os).cons_cons o
    · exact (dedupAux_sublist seen os).cons_cons o

theorem dedupAux_keeps : ∀ (seen : List (Name × Nat)) (l : List Occ), ∀ o ∈ l,
    (o.kind ≠ .bind → o ∈ dedupAux seen l) ∧
    (o.kind = .bind → (o.n, o.scope) ∈ seen ∨
      ∃ b ∈ dedupAux seen l, b.kind = .bind ∧ b.n = o.n ∧ b.scope = o.scope)
  | _, [], o, ho => by simp at ho
  | seen, x :: os, o, ho => by
    simp only [List.mem_cons] at ho
    unfold dedupAux
    rcases ho with rfl | ho
    · by_cases hk : o.kind = .bind
      · rw [ite_eq_left hk]
        refine ⟨fun h => absurd hk h, fun _ => ?_⟩
        by_cases hs : (o.n, o.scope) ∈ seen
        · exact Or.inl hs
        · rw [ite_eq_right hs]
          exact Or.inr ⟨o, List.mem_cons_self, hk, rfl, rfl⟩
      · rw [ite_eq_right hk]
        exact ⟨fun _ => List.mem_cons_self, fun h => absurd h hk⟩
    · have ih := dedupAux_keeps seen os o ho
      by_cases hk : x.kind = .bind
      · rw [ite_eq_left hk]
        by_cases hs : (x.n, x.scope) ∈ seen
        · rw [ite_eq_left hs]; exact ih
        · rw [ite_eq_right hs]
          have ih2 := dedupAux_keeps ((x.n, x.scope) :: seen) os o ho
          refine ⟨fun h => List.mem_cons_of_mem _ (ih2.1 h), fun h => ?_⟩
          rcases ih2.2 h with hm | ⟨b, hb, hh⟩
          · simp only [List.mem_cons] at hm
            rcases hm with e | hm
            · exact Or.inr ⟨x, List.mem_cons_self, hk, (Prod.mk.inj e).1.symm, (Prod.mk.inj e).2.symm⟩
            · exact Or.inl hm
          · exact Or.inr ⟨b, List.mem_cons_of_mem _ hb, hh⟩
      · rw [ite_eq_right hk]
        refine ⟨fun h => List.mem_cons_of_mem _ (ih.1 h), fun h => ?_⟩
        rcases ih.2 h with hm | ⟨b, hb, hh⟩
        · exact Or.inl hm
        · exact Or.inr ⟨b, List.mem_cons_of_mem _ hb, hh⟩

theorem dedupAux_unique : ∀ (seen : List (Name × Nat)) (l : List Occ),
    (bindPairs (dedupAux seen l)).Nodup ∧ ∀ p ∈ bindPairs (dedupAux seen l), p ∉ seen
  | _, [] => by simp [dedupAux, bindPairs]
  | seen, o :: os => by
    unfold dedupAux
    by_cases hk : o.kind = .bind
    · rw [ite_eq_left hk]
      by_cases hs : (o.n, o.scope) ∈ seen
      · rw [ite_eq_left hs]; exact dedupAux_unique seen os
      · rw [ite_eq_right hs]
        have ih := dedupAux_unique ((o.n, o.scope) :: seen) os
        have hpairs : bindPairs (o :: dedupAux ((o.n, o.scope) :: seen) os) =
            (o.n, o.scope) :: bindPairs (dedupAux ((o.n, o.scope) :: seen) os) := by
          simp [bindPairs, hk]
        rw [hpairs]
        refine ⟨List.nodup_cons.mpr ⟨fun hm => ih.2 _ hm List.mem_cons_self, ih.1⟩, ?_⟩
        intro p hp
        simp only [List.mem_cons] at hp
        rcases hp with rfl | hp
        · exact hs
        · exact fun hm => ih.2 p hp (List.mem_cons_of_mem _ hm)
    · rw [ite_eq_right hk]
      have ih := dedupAux_unique seen os
      have hpairs : bindPairs (o :: dedupAux seen os) = bindPairs (dedupAux seen os) := by
        simp [bindPairs, hk]
      rw [hpairs]; exact ih

/-- The compiler output of R5: `build`, with one binding per variable and scope. -/
def emit (q : Spec) : List Occ := dedup (build cand q).1

theorem emit_sub (q : Spec) : ∀ o ∈ emit cand q, o ∈ (build cand q).1 :=
  fun _ ho => (dedupAux_sublist [] _).subset ho

theorem emit_bind (q : Spec) (b : Occ) (hb : b ∈ (build cand q).1) (hk : b.kind = .bind) :
    ∃ b' ∈ emit cand q, b'.kind = .bind ∧ b'.n = b.n ∧ b'.scope = b.scope := by
  rcases (dedupAux_keeps [] _ b hb).2 hk with h | h
  · simp at h
  · exact h

/-- R5: each variable has exactly one binding in each scope. -/
theorem emit_one_binding (q : Spec) : (bindPairs (emit cand q)).Nodup :=
  (dedupAux_unique [] _).1

/-- Every use is still bound in its own scope. -/
theorem emit_uses_bound (q : Spec) : UsesBound (emit cand q) := by
  intro o ho hk
  obtain ⟨b, hb, hbk, hn, hs⟩ := build_uses_bound cand q o (emit_sub cand q o ho) hk
  obtain ⟨b', hb', hk', hn', hs'⟩ := emit_bind cand q b hb hbk
  exact ⟨b', hb', hk', hn'.trans hn, hs'.trans hs⟩

/-- Every owner use is still bound in the same or an enclosing scope. -/
theorem emit_owners_bound (q : Spec) :
    ∀ o ∈ emit cand q, o.kind = .ownerUse →
      ∃ b ∈ emit cand q, b.kind = .bind ∧ b.n = o.n ∧ b.scope ∈ o.path := by
  intro o ho hk
  obtain ⟨b, hb, hbk, hn, hs⟩ := build_owners_bound cand q o (emit_sub cand q o ho) hk
  obtain ⟨b', hb', hk', hn', hs'⟩ := emit_bind cand q b hb hbk
  exact ⟨b', hb', hk', hn'.trans hn, hs' ▸ hs⟩

theorem emit_same_name_same_family (q : Spec) :
    ∀ o₁ ∈ emit cand q, ∀ o₂ ∈ emit cand q, o₁.n = o₂.n → o₁.fam = o₂.fam :=
  fun o₁ h₁ o₂ h₂ he => build_same_name_same_family cand q o₁ (emit_sub cand q _ h₁) o₂ (emit_sub cand q _ h₂) he

theorem emit_outputs_unique (q : Spec) :
    (famBindNames .result (emit cand q)).Nodup ∧
    (famBindNames .reduce (emit cand q)).Nodup ∧
    (famBindNames .old (emit cand q)).Nodup := by
  have hs := dedupAux_sublist [] (build cand q).1
  have h := build_outputs_unique cand q
  have sub : ∀ fam, (famBindNames fam (emit cand q)).Sublist (famBindNames fam (build cand q).1) :=
    fun fam => (hs.filter _).map _
  exact ⟨(sub _).nodup h.1, (sub _).nodup h.2.1, (sub _).nodup h.2.2⟩

theorem emit_names_valid (q : Spec) (hf : WFTree q.filter) (hl : ∀ l ∈ q.sorts ++ q.reduces, Bytes l) :
    ∀ o ∈ emit designCand q, ValidVar o.n :=
  fun o ho => build_names_valid q hf hl o (emit_sub designCand q o ho)

/-- R5 on the R2 example: `And(Gt("age", 1), Lt("age", 5))` binds `age` once. -/
example :
    (bindPairs (emit designCand ⟨.and (.cmp ageL) (.cmp ageL), [], [], 0⟩)).length = 2 := by
  decide

/-- Deduplication does not merge scopes: the R2 example still binds `age` in
scope 0 and in the `not` body (scope 1). -/
example :
    ((emit designCand ⟨.and (.cmp ageL) (.not (.cmp ageL)), [], [], 0⟩).filter
      (fun o => o.fam == .attr && o.kind == .bind)).map (fun o => (o.n, o.scope)) =
    [(bytes "e__age", 0), (bytes "e_s1__age", 1)] := by
  decide

end Scopes
