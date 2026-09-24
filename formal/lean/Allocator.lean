import Naming
import Std.Data.String.ToNat

set_option autoImplicit false

/-!
# Variable allocator for issue #138

A model of the per-query variable allocator in issue #138. The public API is
sealed and typed (`Attr`, typed `Computed`), so callers never write variable
names. Every variable comes from the allocator. Names are byte lists
(`List Nat`), as in `VarLabel`.

Proven here:

* `alloc_run_inv`, `inv_distinct`: for any sequence of allocations, distinct
  keys get distinct names.
* `alloc_memo`: the same key always gets the same name (attribute sharing and
  player sharing in a scope).
* `pick_fresh`, `pick_form`: the suffix search always finds a free name, and
  the result is the candidate or the candidate with `_<n>`, n ≥ 2.
* `run_valid`: with valid candidates, every generated name is a valid TypeQL
  variable spelling (fault 6 of the issue). `rootSpelling_valid` and
  `attrName_valid` show that the readable candidates are valid.
* `inv_dunder`: with conforming candidates, a generated name contains `__` if,
  and only if, its key is an attribute key. This is a readability property.
  The names are not part of the public contract.
* `rootSpelling_injective`: distinct valid role labels get distinct readable
  player candidates.

The example at the end shows that generated names can depend on the order of
allocation. The meaning of the query does not.

`Scopes.lean` models the compiler that walks a filter tree and makes the keys.

Check with: `cd formal/lean && lake build`
-/

namespace Allocator

open VarLabel (us hy isAlnum esc unesc unesc_esc varLabel isPlain)

abbrev Name := List Nat

/-! ## Suffixes: `_<n>` from `_2` -/

def digits (n : Nat) : Name := (Nat.toDigits 10 n).map Char.toNat

theorem map_toNat_inj : ∀ {a b : List Char}, a.map Char.toNat = b.map Char.toNat → a = b
  | [], [], _ => rfl
  | [], _ :: _, h => by simp at h
  | _ :: _, [], h => by simp at h
  | x :: xs, y :: ys, h => by
    simp only [List.map_cons, List.cons.injEq] at h
    rw [Char.toNat_inj.mp h.1, map_toNat_inj h.2]

theorem digits_inj {a b : Nat} (h : digits a = digits b) : a = b := by
  have h1 : Nat.toDigits 10 a = Nat.toDigits 10 b := map_toNat_inj h
  apply Nat.repr_inj.mp
  rw [Nat.repr_eq_ofList_toDigits, Nat.repr_eq_ofList_toDigits, h1]

/-- Every byte of `digits n` is an ASCII digit (48 to 57). -/
theorem digits_are_digits (n : Nat) : ∀ x ∈ digits n, 48 ≤ x ∧ x ≤ 57 := by
  intro x hx
  simp only [digits, List.mem_map] at hx
  obtain ⟨c, hc, rfl⟩ := hx
  have := Nat.isDigit_of_mem_toDigits (by decide) (by decide) hc
  simp [Char.isDigit] at this
  have h1 : (48 : UInt32) ≤ c.val := this.1
  have h2 : c.val ≤ (57 : UInt32) := this.2
  simp only [Char.toNat]
  rw [UInt32.le_iff_toNat_le] at h1 h2
  simp at h1 h2
  exact ⟨h1, h2⟩

theorem digits_ne_nil (n : Nat) : digits n ≠ [] := by
  simp [digits, Nat.toDigits_ne_nil]

def suffixed (c : Name) (n : Nat) : Name := c ++ us :: digits n

theorem suffixed_inj {c : Name} {a b : Nat} (h : suffixed c a = suffixed c b) : a = b := by
  simp only [suffixed] at h
  have := List.append_cancel_left h
  simp only [List.cons.injEq, true_and] at this
  exact digits_inj this

theorem suffixed_ne (c : Name) (n : Nat) : suffixed c n ≠ c := by
  intro h
  have := congrArg List.length h
  simp [suffixed] at this

/-! ## The suffix search

The allocator tries the readable candidate `c`, then `c_2`, `c_3`, and more,
and takes the first name that is not used. With `L` used names, the first
`L + 1` candidates always contain a free one (pigeonhole). -/

def candidates (c : Name) (L : Nat) : List Name :=
  c :: (List.range (L + 1)).map (fun i => suffixed c (i + 2))

theorem candidates_nodup (c : Name) (L : Nat) : (candidates c L).Nodup := by
  simp only [candidates, List.nodup_cons, List.mem_map, List.mem_range, not_exists, not_and]
  refine ⟨fun i _ h => suffixed_ne c (i + 2) h, ?_⟩
  apply List.Pairwise.map _ _ List.nodup_range
  intro a b hab h
  exact hab (by have := suffixed_inj h; omega)

theorem candidates_length (c : Name) (L : Nat) : (candidates c L).length = L + 2 := by
  simp [candidates]

/-- Pigeonhole: more distinct candidates than used names leaves one free. -/
theorem exists_not_mem {α : Type} [DecidableEq α] :
    ∀ (cs used : List α), cs.Nodup → used.length < cs.length → ∃ x ∈ cs, x ∉ used
  | [], _, _, h => by simp at h
  | c :: cs, used, hnd, hlen => by
    by_cases hc : c ∈ used
    · have hnd' := (List.nodup_cons.mp hnd)
      have hlen' : (used.erase c).length < cs.length := by
        have hpos := List.length_pos_of_mem hc
        rw [List.length_erase_of_mem hc]; simp at hlen; omega
      obtain ⟨x, hx, hxu⟩ := exists_not_mem cs (used.erase c) hnd'.2 hlen'
      have hxc : x ≠ c := fun e => hnd'.1 (e ▸ hx)
      exact ⟨x, List.mem_cons_of_mem _ hx, fun hu => hxu ((List.mem_erase_of_ne hxc).mpr hu)⟩
    · exact ⟨c, List.mem_cons_self, hc⟩

def pick (c : Name) (used : List Name) : Name :=
  ((candidates c used.length).find? (fun x => !used.contains x)).getD c

theorem pick_fresh (c : Name) (used : List Name) : pick c used ∉ used := by
  obtain ⟨x, hx, hxu⟩ := exists_not_mem (candidates c used.length) used
    (candidates_nodup c _) (by rw [candidates_length]; omega)
  unfold pick
  cases hf : (candidates c used.length).find? (fun x => !used.contains x) with
  | none =>
    rw [List.find?_eq_none] at hf
    exact absurd (by simpa using hxu) (hf x hx)
  | some y =>
    have := List.find?_some hf
    simpa using this

/-- The result is the candidate, or the candidate with a suffix `_<n>`, n ≥ 2. -/
theorem pick_form (c : Name) (used : List Name) :
    pick c used = c ∨ ∃ n, 2 ≤ n ∧ pick c used = suffixed c n := by
  unfold pick
  cases hf : (candidates c used.length).find? (fun x => !used.contains x) with
  | none => exact Or.inl rfl
  | some y =>
    have hm := List.mem_of_find?_eq_some hf
    simp only [candidates, List.mem_cons, List.mem_map, List.mem_range] at hm
    rcases hm with rfl | ⟨i, _, rfl⟩
    · exact Or.inl rfl
    · exact Or.inr ⟨i + 2, by omega, rfl⟩

/-! ## The `__` test -/

def hasDU : Name → Bool
  | a :: b :: r => (a == us && b == us) || hasDU (b :: r)
  | _ => false

theorem hasDU_append_left : ∀ (a b : Name), hasDU a = true → hasDU (a ++ b) = true
  | [], _, h => by simp [hasDU] at h
  | [_], _, h => by simp [hasDU] at h
  | x :: y :: r, b, h => by
    simp only [hasDU, Bool.or_eq_true] at h
    simp only [List.cons_append, hasDU, Bool.or_eq_true]
    rcases h with h | h
    · exact Or.inl h
    · exact Or.inr (by simpa using hasDU_append_left (y :: r) b h)

theorem hasDU_append_right : ∀ (a b : Name), hasDU b = true → hasDU (a ++ b) = true
  | [], _, h => h
  | x :: r, b, h => by
    have ih := hasDU_append_right r b h
    cases hr : r ++ b with
    | nil => rw [hr] at ih; simp [hasDU] at ih
    | cons y t =>
      simp only [List.cons_append, hr, hasDU, Bool.or_eq_true]
      exact Or.inr (hr ▸ ih)

/-- A `__` in `a ++ b` lies in `a`, in `b`, or across the junction. -/
theorem hasDU_append : ∀ (a b : Name), hasDU (a ++ b) = true →
    hasDU a = true ∨ hasDU b = true ∨ (a.getLast? = some us ∧ b.head? = some us)
  | [], _, h => Or.inr (Or.inl h)
  | [x], b, h => by
    cases b with
    | nil => simp [hasDU] at h
    | cons y t =>
      simp only [List.cons_append, List.nil_append, hasDU, Bool.or_eq_true, Bool.and_eq_true,
        beq_iff_eq] at h
      rcases h with ⟨rfl, rfl⟩ | h
      · exact Or.inr (Or.inr ⟨rfl, rfl⟩)
      · exact Or.inr (Or.inl h)
  | x :: y :: r, b, h => by
    simp only [List.cons_append, hasDU, Bool.or_eq_true] at h
    rcases h with h | h
    · exact Or.inl (by simp [hasDU, h])
    · have := hasDU_append (y :: r) b (by simpa using h)
      rcases this with h1 | h1 | h1
      · exact Or.inl (by simp [hasDU, h1])
      · exact Or.inr (Or.inl h1)
      · exact Or.inr (Or.inr ⟨by simpa [List.getLast?_cons_cons] using h1.1, h1.2⟩)

theorem not_hasDU_of_no_us : ∀ (a : Name), (∀ x ∈ a, x ≠ us) → hasDU a = false
  | [], _ => rfl
  | [_], _ => rfl
  | x :: y :: r, h => by
    have hx : x ≠ us := h x (by simp)
    have ih := not_hasDU_of_no_us (y :: r) (fun z hz => h z (List.mem_cons_of_mem _ hz))
    simp [hasDU, hx, ih]

theorem digits_no_us (n : Nat) : ∀ x ∈ digits n, x ≠ us := by
  intro x hx heq
  have := digits_are_digits n x hx
  simp [us] at heq; omega

theorem digits_head (n : Nat) : ∃ d, (digits n).head? = some d ∧ d ≠ us := by
  cases h : digits n with
  | nil => exact absurd h (digits_ne_nil n)
  | cons d t => exact ⟨d, rfl, digits_no_us n d (by simp [h])⟩

theorem digits_last (n : Nat) : ∃ d, (digits n).getLast? = some d ∧ d ≠ us := by
  cases h : (digits n).getLast? with
  | none => simp [List.getLast?_eq_none_iff] at h; exact absurd h (digits_ne_nil n)
  | some d => exact ⟨d, rfl, digits_no_us n d (List.mem_of_getLast? h)⟩

theorem getLast?_cons_ne_nil (x : Nat) : ∀ (b : Name), b ≠ [] → (x :: b).getLast? = b.getLast?
  | [], h => absurd rfl h
  | _ :: _, _ => by simp [List.getLast?_cons_cons]

theorem getLast?_append_cons : ∀ (a : Name) (x : Nat) (b : Name),
    (a ++ x :: b).getLast? = (x :: b).getLast?
  | [], _, _ => rfl
  | y :: a, x, b => by
    rw [List.cons_append, getLast?_cons_ne_nil y (a ++ x :: b) (by simp), getLast?_append_cons a x b]

/-- A conforming name: no `__`, and it does not end with `_`. -/
def Conforming (c : Name) : Prop := hasDU c = false ∧ c.getLast? ≠ some us

theorem suffixed_conforming (c : Name) (n : Nat) (hc : Conforming c) :
    Conforming (suffixed c n) := by
  obtain ⟨d, hd, hdu⟩ := digits_head n
  obtain ⟨e, he, heu⟩ := digits_last n
  constructor
  · cases h : hasDU (suffixed c n) with
    | false => rfl
    | true =>
      exfalso
      unfold suffixed at h
      rcases hasDU_append c (us :: digits n) h with h1 | h1 | h1
      · simp [hc.1] at h1
      · -- `_` followed by the digits: no `__`
        cases hdn : digits n with
        | nil => exact digits_ne_nil n hdn
        | cons d' t =>
          rw [hdn] at h1 hd
          simp only [Option.some.injEq, List.head?_cons] at hd
          subst hd
          simp only [hasDU, Bool.or_eq_true, Bool.and_eq_true, beq_iff_eq] at h1
          rcases h1 with ⟨_, h2⟩ | h2
          · exact hdu h2
          · rw [not_hasDU_of_no_us (d' :: t) (hdn ▸ digits_no_us n)] at h2; simp at h2
      · exact hc.2 h1.1
  · unfold suffixed
    rw [getLast?_append_cons]
    cases hdn : digits n with
    | nil => exact absurd hdn (digits_ne_nil n)
    | cons d' t =>
      rw [getLast?_cons_ne_nil us (d' :: t) (by simp), ← hdn, he]
      intro heq; exact heu (Option.some.inj heq)

/-! ## Allocator state

The table maps keys to names. There is one table for each query, for all
scopes and families. `isAttr` marks attribute keys. -/

variable {K : Type} [DecidableEq K]

structure State (K : Type) where
  table : List (K × Name)

def used (st : State K) : List Name := st.table.map Prod.snd

def alloc (cand : K → Name) (st : State K) (k : K) : Name × State K :=
  match st.table.lookup k with
  | some n => (n, st)
  | none =>
    let n := pick (cand k) (used st)
    (n, { st with table := (k, n) :: st.table })

def run (cand : K → Name) (st : State K) (ks : List K) : State K :=
  ks.foldl (fun st k => (alloc cand st k).2) st

/-- Distinct keys and distinct names. -/
structure Inv (st : State K) : Prop where
  keys_nodup : (st.table.map Prod.fst).Nodup
  names_nodup : (st.table.map Prod.snd).Nodup

omit [DecidableEq K] in
theorem inv_start : Inv ({ table := [] } : State K) :=
  ⟨List.nodup_nil, List.nodup_nil⟩

theorem lookup_none_not_mem : ∀ (t : List (K × Name)) (k : K),
    t.lookup k = none → k ∉ t.map Prod.fst
  | [], _, _ => by simp
  | (k', n) :: t, k, h => by
    by_cases hk : k = k'
    · subst hk; simp [List.lookup] at h
    · have hk' : (k == k') = false := by simp [hk]
      simp only [List.lookup, hk'] at h
      have := lookup_none_not_mem t k h
      simp only [List.map_cons, List.mem_cons, not_or]
      exact ⟨hk, this⟩

theorem alloc_inv (cand : K → Name) (st : State K) (k : K) (h : Inv st) :
    Inv (alloc cand st k).2 := by
  unfold alloc
  cases hl : st.table.lookup k with
  | some n => exact h
  | none =>
    have hk := lookup_none_not_mem st.table k hl
    have hfresh := pick_fresh (cand k) (used st)
    simp only [used] at hfresh
    exact ⟨List.nodup_cons.mpr ⟨hk, h.keys_nodup⟩, List.nodup_cons.mpr ⟨hfresh, h.names_nodup⟩⟩

/-- Every sequence of allocations keeps the invariant. -/
theorem alloc_run_inv (cand : K → Name) :
    ∀ (ks : List K) (st : State K), Inv st → Inv (run cand st ks)
  | [], _, h => h
  | k :: ks, st, h => alloc_run_inv cand ks _ (alloc_inv cand st k h)

theorem map_nodup_inj {α β : Type} (f : α → β) : ∀ (l : List α), (l.map f).Nodup →
    ∀ a ∈ l, ∀ b ∈ l, f a = f b → a = b
  | [], _, a, ha, _, _, _ => by simp at ha
  | x :: l, hnd, a, ha, b, hb, hab => by
    simp only [List.map_cons, List.nodup_cons, List.mem_map, not_exists, not_and] at hnd
    simp only [List.mem_cons] at ha hb
    rcases ha with rfl | ha <;> rcases hb with rfl | hb
    · rfl
    · exact absurd hab.symm (hnd.1 b hb)
    · exact absurd hab (hnd.1 a ha)
    · exact map_nodup_inj f l hnd.2 a ha b hb hab

omit [DecidableEq K] in
/-- Two different keys in the table never share a name. -/
theorem inv_distinct (st : State K) (h : Inv st) (k₁ k₂ : K) (n : Name)
    (h₁ : (k₁, n) ∈ st.table) (h₂ : (k₂, n) ∈ st.table) : k₁ = k₂ := by
  have := map_nodup_inj Prod.snd st.table h.names_nodup _ h₁ _ h₂ rfl
  simp at this; exact this

/-- The same key always gets the same name (C7, C8). -/
theorem alloc_memo (cand : K → Name) (st : State K) (k : K) :
    ((alloc cand st k).2.table.lookup k) = some (alloc cand st k).1 := by
  unfold alloc
  cases hl : st.table.lookup k with
  | some n => simpa using hl
  | none => simp [List.lookup]

/-! ## `__` if, and only if, the key is an attribute key (readability) -/

/-- Conforming candidates: attribute candidates contain `__`, the others conform. -/
structure ConformingCand (cand : K → Name) (isAttr : K → Bool) : Prop where
  attr : ∀ k, isAttr k = true → hasDU (cand k) = true
  other : ∀ k, isAttr k = false → Conforming (cand k)

def DunderInv (isAttr : K → Bool) (st : State K) : Prop :=
  ∀ p ∈ st.table, hasDU p.2 = isAttr p.1

omit [DecidableEq K] in
theorem pick_dunder (cand : K → Name) (isAttr : K → Bool) (hc : ConformingCand cand isAttr)
    (k : K) (u : List Name) : hasDU (pick (cand k) u) = isAttr k := by
  rcases pick_form (cand k) u with h | ⟨m, _, h⟩ <;> rw [h]
  · cases ha : isAttr k
    · exact (hc.other k ha).1
    · exact hc.attr k ha
  · cases ha : isAttr k
    · exact (suffixed_conforming _ m (hc.other k ha)).1
    · exact hasDU_append_left _ _ (hc.attr k ha)

theorem alloc_dunder (cand : K → Name) (isAttr : K → Bool) (hc : ConformingCand cand isAttr)
    (st : State K) (k : K) (h : DunderInv isAttr st) : DunderInv isAttr (alloc cand st k).2 := by
  unfold alloc
  cases hl : st.table.lookup k with
  | some n => exact h
  | none =>
    intro p hp
    simp only [List.mem_cons] at hp
    rcases hp with rfl | hp
    · exact pick_dunder cand isAttr hc k _
    · exact h p hp

theorem inv_dunder (cand : K → Name) (isAttr : K → Bool) (hc : ConformingCand cand isAttr) :
    ∀ (ks : List K) (st : State K), DunderInv isAttr st → DunderInv isAttr (run cand st ks)
  | [], _, h => h
  | k :: ks, st, h => inv_dunder cand isAttr hc ks _ (alloc_dunder cand isAttr hc st k h)

/-! ## `rootSpelling`: the readable player candidate

`rootSpelling(role)` is `naming.VarLabel(role)` when that result starts with a
letter or digit, contains no `__`, and does not end with `_`. Otherwise it is
`0` followed by the label with the `VarLabel` escapes. -/

open VarLabel (escChar hexDigit enc)

def rootSpelling (l : Name) : Name :=
  let v := varLabel l
  if (v.head?.map isAlnum = some true) && !hasDU v && v.getLast? != some us then v
  else 48 :: esc l

def isLetter (c : Nat) : Bool := (97 ≤ c && c ≤ 122) || (65 ≤ c && c ≤ 90)

/-- A valid label (`[a-zA-Z][a-zA-Z0-9_-]*`, as `validateAttrName` checks) over bytes. -/
def ValidLabel (l : Name) : Prop := (∃ c r, l = c :: r ∧ isLetter c = true) ∧ ∀ x ∈ l, x < 256

theorem hexDigit_ne_us (d : Nat) : hexDigit d ≠ us := by
  unfold hexDigit us; split <;> omega

theorem escChar_no_dunder (c : Nat) : hasDU (escChar c) = false := by
  unfold escChar
  split
  · decide
  · split
    · decide
    · split
      · rfl
      · have h1 : hexDigit (c / 16) ≠ 95 := hexDigit_ne_us _
        have h2 : hexDigit (c % 16) ≠ 95 := hexDigit_ne_us _
        simp [hasDU, us, h1, h2]

theorem escChar_head_ne_us_or (c : Nat) : ∀ x, (escChar c).getLast? = some x → x ≠ us := by
  intro x hx
  unfold escChar at hx
  split at hx
  · simp at hx; subst hx; decide
  · split at hx
    · simp at hx; subst hx; decide
    · split at hx
      · rename_i h1 h2 h3; simp at hx; subst hx; intro e; subst e; simp [VarLabel.us_not_alnum] at h3
      · simp [List.getLast?_cons_cons] at hx; subst hx; exact hexDigit_ne_us _

theorem esc_no_dunder : ∀ (l : Name), hasDU (esc l) = false
  | [] => rfl
  | c :: r => by
    cases h : hasDU (esc (c :: r)) with
    | false => rfl
    | true =>
      exfalso
      simp only [VarLabel.esc] at h
      rcases hasDU_append _ _ h with h1 | h1 | h1
      · rw [escChar_no_dunder] at h1; simp at h1
      · rw [esc_no_dunder r] at h1; simp at h1
      · exact escChar_head_ne_us_or c us h1.1 rfl

theorem getLast?_append_ne_nil : ∀ (a b : Name), b ≠ [] → (a ++ b).getLast? = b.getLast?
  | _, [], h => absurd rfl h
  | a, x :: b, _ => getLast?_append_cons a x b

theorem esc_last_ne_us : ∀ (l : Name), ∀ x, (esc l).getLast? = some x → x ≠ us
  | [], x, h => by simp [VarLabel.esc] at h
  | c :: r, x, h => by
    simp only [VarLabel.esc] at h
    cases hr : esc r with
    | nil =>
      rw [hr, List.append_nil] at h
      exact escChar_head_ne_us_or c x h
    | cons y t =>
      rw [hr, getLast?_append_ne_nil _ _ (by simp), ← hr] at h
      exact esc_last_ne_us r x h

theorem rootSpelling_no_dunder (l : Name) : hasDU (rootSpelling l) = false := by
  simp only [rootSpelling]
  split
  · rename_i h; simp only [Bool.and_eq_true, Bool.not_eq_true'] at h; exact h.1.2
  · cases h : hasDU (48 :: esc l) with
    | false => rfl
    | true =>
      exfalso
      have := hasDU_append [48] (esc l) h
      rcases this with h1 | h1 | h1
      · simp [hasDU] at h1
      · rw [esc_no_dunder] at h1; simp at h1
      · simp [us] at h1

theorem rootSpelling_head (l : Name) : ∀ x, (rootSpelling l).head? = some x → x ≠ us := by
  intro x hx
  simp only [rootSpelling] at hx
  split at hx
  · rename_i h
    simp only [Bool.and_eq_true, Bool.not_eq_true'] at h
    have h1 := h.1.1
    rw [hx] at h1
    simp at h1
    intro e; subst e; simp [VarLabel.us_not_alnum] at h1
  · simp at hx; subst hx; simp [us]

theorem rootSpelling_last (l : Name) : ∀ x, (rootSpelling l).getLast? = some x → x ≠ us := by
  intro x hx
  simp only [rootSpelling] at hx
  split at hx
  · rename_i h
    simp only [Bool.and_eq_true, Bool.not_eq_true', bne_iff_ne, ne_eq] at h
    intro e; subst e; exact h.2 hx
  · cases he : esc l with
    | nil => rw [he] at hx; simp at hx; subst hx; simp [us]
    | cons y t =>
      rw [he, getLast?_cons_ne_nil 48 _ (by simp), ← he] at hx
      exact esc_last_ne_us l x hx

theorem varLabel_plain_head (c : Nat) (r : Name) (h : isLetter c = true) :
    (varLabel (c :: r)).head? = some c ∨ (varLabel (c :: r)).head? = some us := by
  have hc : c ≠ 45 := by intro e; subst e; simp [isLetter] at h
  unfold varLabel
  split
  · left; simp [enc, VarLabel.hy, hc]
  · right; rfl

theorem esc_inj (a b : Name) (ha : ∀ x ∈ a, x < 256) (hb : ∀ x ∈ b, x < 256)
    (h : esc a = esc b) : a = b := by
  rw [← unesc_esc a ha, ← unesc_esc b hb, h]

/-- Item 4: distinct valid role labels get distinct root spellings. -/
theorem rootSpelling_injective (a b : Name) (ha : ValidLabel a) (hb : ValidLabel b)
    (h : rootSpelling a = rootSpelling b) : a = b := by
  obtain ⟨⟨ca, ra, rfl, hla⟩, hba⟩ := ha
  obtain ⟨⟨cb, rb, rfl, hlb⟩, hbb⟩ := hb
  -- In the first form, the head is the label's first letter; in the second, it is `0`.
  have headA := varLabel_plain_head ca ra hla
  have headB := varLabel_plain_head cb rb hlb
  simp only [rootSpelling] at h
  split at h <;> split at h
  · exact VarLabel.varLabel_injective _ _ hba hbb h
  · rename_i h1 _
    simp only [Bool.and_eq_true, Bool.not_eq_true'] at h1
    have hh := h1.1.1
    rcases headA with hA | hA <;> rw [hA] at hh
    · rw [h] at hA; simp at hA; subst hA; simp [isLetter] at hla
    · simp [VarLabel.us_not_alnum] at hh
  · rename_i _ h2
    simp only [Bool.and_eq_true, Bool.not_eq_true'] at h2
    have hh := h2.1.1
    rcases headB with hB | hB <;> rw [hB] at hh
    · rw [← h] at hB; simp at hB; subst hB; simp [isLetter] at hlb
    · simp [VarLabel.us_not_alnum] at hh
  · exact esc_inj _ _ hba hbb (List.cons.inj h).2

/-- The examples from the design ("Terms" in issue #138). -/
def bytes (s : String) : Name := s.toList.map Char.toNat
example : rootSpelling (bytes "author") = bytes "author" := by decide
example : rootSpelling (bytes "first-author") = bytes "first_author" := by decide
example : rootSpelling (bytes "first_author") = bytes "0first_uauthor" := by decide
example : rootSpelling (bytes "a--b") = bytes "0a_h_hb" := by decide
example : rootSpelling (bytes "e__age") = bytes "0e_u_uage" := by decide

/-! ## Valid TypeQL variable spellings

TypeQL reads a variable as `$` followed by a letter or digit, then letters,
digits, `_`, or `-` (checked with `typeql-check`). Fault 6 of the issue was a
generated name that started with `_`. -/

def isVarChar (c : Nat) : Bool := isAlnum c || c == us || c == hy

def ValidVar (n : Name) : Prop := (∃ c r, n = c :: r ∧ isAlnum c = true) ∧ ∀ x ∈ n, isVarChar x = true

theorem digits_alnum (n : Nat) : ∀ x ∈ digits n, isAlnum x = true := by
  intro x hx
  have := digits_are_digits n x hx
  simp [isAlnum]; omega

theorem suffixed_valid (c : Name) (n : Nat) (h : ValidVar c) : ValidVar (suffixed c n) := by
  obtain ⟨⟨c₀, r, rfl, hc₀⟩, hall⟩ := h
  refine ⟨⟨c₀, r ++ us :: digits n, by simp [suffixed], hc₀⟩, ?_⟩
  intro x hx
  simp only [suffixed, List.mem_append, List.mem_cons] at hx
  rcases hx with hx | rfl | hx
  · exact hall x (List.mem_cons.mpr hx)
  · simp [isVarChar]
  · simp [isVarChar, digits_alnum n x hx]

theorem pick_valid (c : Name) (u : List Name) (h : ValidVar c) : ValidVar (pick c u) := by
  rcases pick_form c u with e | ⟨m, _, e⟩ <;> rw [e]
  · exact h
  · exact suffixed_valid c m h

def ValidCand (cand : K → Name) : Prop := ∀ k, ValidVar (cand k)

def ValidTable (st : State K) : Prop := ∀ p ∈ st.table, ValidVar p.2

theorem alloc_valid (cand : K → Name) (hc : ValidCand cand) (st : State K) (k : K)
    (h : ValidTable st) : ValidTable (alloc cand st k).2 := by
  unfold alloc
  cases hl : st.table.lookup k with
  | some n => exact h
  | none =>
    intro p hp
    simp only [List.mem_cons] at hp
    rcases hp with rfl | hp
    · exact pick_valid _ _ (hc k)
    · exact h p hp

/-- With valid candidates, every generated name is a valid TypeQL variable. -/
theorem run_valid (cand : K → Name) (hc : ValidCand cand) :
    ∀ (ks : List K) (st : State K), ValidTable st → ValidTable (run cand st ks)
  | [], _, h => h
  | k :: ks, st, h => run_valid cand hc ks _ (alloc_valid cand hc st k h)

theorem hexDigit_alnum (d : Nat) (h : d < 16) : isAlnum (hexDigit d) = true := by
  unfold hexDigit; split <;> simp [isAlnum] <;> omega

theorem escChar_chars (c : Nat) (hc : c < 256) : ∀ x ∈ escChar c, isVarChar x = true := by
  intro x hx
  unfold escChar at hx
  split at hx
  · simp at hx; rcases hx with rfl | rfl <;> decide
  · split at hx
    · simp at hx; rcases hx with rfl | rfl <;> decide
    · split at hx
      · rename_i h; simp at hx; subst hx; simp [isVarChar, h]
      · simp at hx
        rcases hx with rfl | rfl | rfl | rfl
        · decide
        · decide
        · simp [isVarChar, hexDigit_alnum (c / 16) (by omega)]
        · simp [isVarChar, hexDigit_alnum (c % 16) (Nat.mod_lt _ (by omega))]

theorem esc_chars : ∀ (l : Name), (∀ x ∈ l, x < 256) → ∀ x ∈ esc l, isVarChar x = true
  | [], _, x, hx => by simp [VarLabel.esc] at hx
  | c :: r, hb, x, hx => by
    simp only [VarLabel.esc, List.mem_append] at hx
    rcases hx with hx | hx
    · exact escChar_chars c (hb c (by simp)) x hx
    · exact esc_chars r (fun y hy => hb y (List.mem_cons_of_mem _ hy)) x hx

theorem varLabel_chars (l : Name) (hb : ∀ x ∈ l, x < 256) :
    ∀ x ∈ varLabel l, isVarChar x = true := by
  intro x hx
  unfold varLabel at hx
  split at hx
  · rename_i hp
    simp only [List.mem_map] at hx
    obtain ⟨y, hmem, rfl⟩ := hx
    cases l with
    | nil => simp [isPlain] at hp
    | cons c r =>
      simp only [isPlain, Bool.and_eq_true, List.all_eq_true, Bool.or_eq_true, beq_iff_eq] at hp
      rcases hp.2 y hmem with e | ha
      · subst e; decide
      · have hne : y ≠ hy := fun e => by subst e; simp [isAlnum, VarLabel.hy] at ha
        simp [enc, hne, isVarChar, ha]
  · simp only [List.mem_cons] at hx
    rcases hx with rfl | hx
    · decide
    · exact esc_chars l hb x hx

/-- The readable player candidate is a valid TypeQL variable. -/
theorem rootSpelling_valid (l : Name) (hl : ValidLabel l) : ValidVar (rootSpelling l) := by
  simp only [rootSpelling]
  split
  · rename_i h
    simp only [Bool.and_eq_true, Bool.not_eq_true'] at h
    have hh := h.1.1
    cases hv : varLabel l with
    | nil => rw [hv] at hh; simp at hh
    | cons c r =>
      rw [hv] at hh
      simp at hh
      refine ⟨⟨c, r, rfl, hh⟩, ?_⟩
      rw [← hv]; exact varLabel_chars l hl.2
  · refine ⟨⟨48, esc l, rfl, by decide⟩, ?_⟩
    intro x hx
    simp only [List.mem_cons] at hx
    rcases hx with rfl | hx
    · decide
    · exact esc_chars l hl.2 x hx

/-- The readable attribute candidate `<owner>__<VarLabel(label)>`. -/
def attrName (owner label : Name) : Name := owner ++ [us, us] ++ varLabel label

theorem attrName_valid (owner label : Name) (ho : ValidVar owner)
    (hb : ∀ x ∈ label, x < 256) : ValidVar (attrName owner label) := by
  obtain ⟨⟨c, r, rfl, hc⟩, hall⟩ := ho
  refine ⟨⟨c, r ++ [us, us] ++ varLabel label, by simp [attrName], hc⟩, ?_⟩
  intro x hx
  simp only [attrName, List.mem_append, List.mem_cons] at hx
  rcases hx with (hx | rfl | rfl | hx) | hx
  · exact hall x (List.mem_cons.mpr hx)
  · decide
  · decide
  · simp at hx
  · exact varLabel_chars label hb x hx

theorem attrName_dunder (owner label : Name) : hasDU (attrName owner label) = true := by
  unfold attrName
  rw [List.append_assoc]
  apply hasDU_append_right
  cases varLabel label <;> simp [hasDU]

/-- The spelling of fault 6 is not a valid variable, so `ValidVar` has teeth. -/
example : ¬ ValidVar (bytes "_first_uauthor") := by
  rintro ⟨⟨c, r, h, hc⟩, _⟩
  have : c = 95 := by
    have := congrArg List.head? h
    simp [bytes] at this
    exact this.symm
  subst this
  simp [isAlnum] at hc

example : ValidVar (rootSpelling (bytes "first_author")) :=
  rootSpelling_valid _ ⟨⟨102, bytes "irst_author", by decide, by decide⟩, by decide⟩

/-! ## Allocation order

The generated names can depend on the order of allocation: when two keys have the same candidate, the key that the
compiler allocates first gets the candidate, and the other gets `_2`. The
meaning of the query does not change, because each key keeps a distinct name
(`alloc_run_inv`). The text of the query does change. -/

example :
    let cand : Nat → Name := fun _ => bytes "x"
    let st : State Nat := { table := [] }
    (run cand st [1, 2]).table.lookup 1 ≠ (run cand st [2, 1]).table.lookup 1 := by
  decide

end Allocator
