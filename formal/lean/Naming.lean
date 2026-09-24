set_option autoImplicit false

/-!
# Naming round trip between tqlgen and gotype

Models of five pure functions (ASCII only, which is what TypeQL labels use):

* `tqlgen.ToPascalCase`          (tqlgen/naming.go)   label   → Go name
* `tqlgen.ToPascalCaseAcronyms`  (tqlgen/naming.go)   label   → Go name
* `gotype.toKebabCase`           (gotype/model.go)    Go name → TypeDB type name
* `gotype.sanitizeVar`           (gotype/filter.go)   label   → TypeQL variable
  (the attribute-variable encoding before the fix)
* `naming.VarLabel`               (internal/naming)    label   → TypeQL variable
  (the fix: proven injective in namespace `VarLabel` below)

Generated structs embed `gotype.BaseEntity` / `gotype.BaseRelation` with no
`type:` override, so the registered type name is
`toKebabCase (ToPascalCase label)`. The theorems below show that this cannot
be the identity: the Go name loses information, so *no* function from Go names
back to labels is correct. This is why tqlgen now emits an explicit `type:` tag. The
old `sanitizeVar` encoding made two filters on distinct attributes share one
TypeQL variable; its replacement `naming.VarLabel` is proven injective.

Check with: `lean formal/lean/Naming.lean`
-/

namespace Naming

def isUpper (c : Char) : Bool := 'A' ≤ c && c ≤ 'Z'
def isLower (c : Char) : Bool := 'a' ≤ c && c ≤ 'z'

/-! ## gotype.toKebabCase -/

/-- One pass of `toKebabCase`; `prev` is the previous rune (none at index 0). -/
def kebab : Option Char → List Char → List Char
  | _, [] => []
  | prev, c :: rest =>
    if isUpper c then
      let startsWord :=
        match prev with
        | none => false
        | some p => !isUpper p || (match rest.head? with
                                   | some n => isLower n
                                   | none => false)
      (if startsWord then ['-'] else []) ++ c.toLower :: kebab (some c) rest
    else
      c :: kebab (some c) rest

def toKebabCase (s : List Char) : List Char := kebab none s

/-! ## tqlgen.ToPascalCase / ToPascalCaseAcronyms -/

def isSep (c : Char) : Bool := c == '-' || c == '_'

/-- `strings.FieldsFunc(name, isSep)`: split on separators, drop empty fields.
`cur` is the current field, reversed. -/
def splitAux : List Char → List Char → List (List Char)
  | [], cur => if cur.isEmpty then [] else [cur.reverse]
  | c :: cs, cur =>
    if isSep c then
      (if cur.isEmpty then [] else [cur.reverse]) ++ splitAux cs []
    else
      splitAux cs (c :: cur)

def splitName (s : List Char) : List (List Char) := splitAux s []

def capitalize : List Char → List Char
  | [] => []
  | c :: cs => c.toUpper :: cs.map Char.toLower

def toPascalCase (s : List Char) : List Char :=
  (splitName s).flatMap capitalize

/-- `tqlgen.CommonAcronyms`. -/
def acronym (lower : List Char) : Option (List Char) :=
  match String.ofList lower with
  | "id" => some "ID".toList
  | "url" => some "URL".toList
  | "uuid" => some "UUID".toList
  | "api" => some "API".toList
  | "http" => some "HTTP".toList
  | "iid" => some "IID".toList
  | "nf" => some "NF".toList
  | _ => none

def capitalizeAcronym (part : List Char) : List Char :=
  let lower := part.map Char.toLower
  match acronym lower with
  | some a => a
  | none => capitalize lower

def toPascalCaseAcronyms (s : List Char) : List Char :=
  (splitName s).flatMap capitalizeAcronym

/-- The TypeDB type name that a generated struct registers under. -/
def registeredName (useAcronyms : Bool) (label : List Char) : List Char :=
  toKebabCase (if useAcronyms then toPascalCaseAcronyms label else toPascalCase label)

/-! ## gotype.sanitizeVar -/

def sanitizeVar (s : List Char) : List Char := s.map (fun c => if c == '-' then '_' else c)

/-- Variable that `ComparisonFilter.ToPatterns "e"` binds for attribute `attr`. -/
def filterVar (attr : List Char) : List Char := sanitizeVar ("e__".toList ++ attr)

/-! ## Sanity: the models agree with the Go code on known-good inputs
(values taken from the Go doc comments and from running the Go code). -/

example : toKebabCase "UserAccount".toList = "user-account".toList := by decide
example : toKebabCase "HTTPServer".toList = "http-server".toList := by decide
example : toKebabCase "User2FA".toList = "user2-fa".toList := by decide
example : toPascalCaseAcronyms "user-id".toList = "UserID".toList := by decide
example : registeredName true "http-server".toList = "http-server".toList := by decide

/-! ## Counterexamples: labels that TypeQL accepts (checked with typeql-check)
but that generated models register under a different name. -/

theorem snake_label_breaks (b : Bool) :
    registeredName b "user_account".toList = "user-account".toList := by
  cases b <;> decide

theorem digit_segment_breaks (b : Bool) :
    registeredName b "tag-2fa".toList = "tag2fa".toList := by
  cases b <;> decide

theorem single_letter_segment_breaks (b : Bool) :
    registeredName b "a-b".toList = "ab".toList := by
  cases b <;> decide

theorem adjacent_acronyms_break :
    registeredName true "http-api".toList = "httpapi".toList := by decide

theorem trailing_acronym_breaks :
    registeredName true "x-http".toList = "xhttp".toList := by decide

/-! ## Impossibility: the Go name does not determine the label

Two distinct labels produce the same Go struct name, so no mapping from Go
names to TypeDB names (the current `toKebabCase` or any replacement) can be
right for both. A correct fix must carry the label itself, for example by
emitting a `type:<label>` tag on the embedded base struct. -/

/-- Generic lemma: a non-injective encoder has no left inverse. -/
theorem no_left_inverse {α β : Type} [DecidableEq α] (enc : α → β) (x y : α)
    (hne : x ≠ y) (hcol : enc x = enc y) (dec : β → α) : ∃ l, dec (enc l) ≠ l := by
  by_cases h : dec (enc x) = x
  · refine ⟨y, ?_⟩
    rw [← hcol, h]
    exact hne
  · exact ⟨x, h⟩

theorem pascal_collides :
    toPascalCase "user_account".toList = toPascalCase "user-account".toList := by decide

theorem pascal_acronyms_collides :
    toPascalCaseAcronyms "user_account".toList = toPascalCaseAcronyms "user-account".toList := by
  decide

/-- No function from generated Go names back to TypeDB labels is correct. -/
theorem generated_name_not_recoverable (dec : List Char → List Char) :
    ∃ l, dec (toPascalCase l) ≠ l :=
  no_left_inverse toPascalCase _ _ (by decide) pascal_collides dec

theorem generated_name_not_recoverable_acronyms (dec : List Char → List Char) :
    ∃ l, dec (toPascalCaseAcronyms l) ≠ l :=
  no_left_inverse toPascalCaseAcronyms _ _ (by decide) pascal_acronyms_collides dec

/-! ## The old filter-variable encoding collides

`first-name` and `first_name` are distinct valid attribute labels, but the old
encoding bound both to `$e__first_name`, which TypeQL treats as an implicit
equality between the two attribute values. -/

theorem filter_vars_collide :
    filterVar "first-name".toList = filterVar "first_name".toList := by decide

theorem filter_var_not_injective :
    ∃ a b, a ≠ b ∧ filterVar a = filterVar b :=
  ⟨"first-name".toList, "first_name".toList, by decide, filter_vars_collide⟩

end Naming

/-! # naming.VarLabel is injective

Model of `internal/naming.VarLabel` over bytes, with a decoder that inverts it.
`varLabel_injective` is the property the fix relies on: distinct labels never
share a generated TypeQL variable. -/

namespace VarLabel

/-! Bytes are modelled as `Nat` (the Go code works on bytes; every lemma that
needs the bound takes `c < 256`). -/

def us : Nat := 95   -- '_'
def hy : Nat := 45   -- '-'

def isAlnum (c : Nat) : Bool :=
  (97 ≤ c && c ≤ 122) || (65 ≤ c && c ≤ 90) || (48 ≤ c && c ≤ 57)

def hexDigit (d : Nat) : Nat := if d < 10 then 48 + d else 87 + d
def unhex (x : Nat) : Nat := if x < 58 then x - 48 else x - 87

def escChar (c : Nat) : List Nat :=
  if c = us then [us, 117]
  else if c = hy then [us, 104]
  else if isAlnum c then [c]
  else [us, 120, hexDigit (c / 16), hexDigit (c % 16)]

def esc : List Nat → List Nat
  | [] => []
  | c :: r => escChar c ++ esc r

def isPlain : List Nat → Bool
  | [] => false
  | c :: r => isAlnum c && (c :: r).all (fun x => x == hy || isAlnum x)

def enc (c : Nat) : Nat := if c = hy then us else c
def dec (c : Nat) : Nat := if c = us then hy else c

/-- `naming.VarLabel`. -/
def varLabel (l : List Nat) : List Nat :=
  if isPlain l then l.map enc else us :: esc l

def unesc : List Nat → List Nat
  | [] => []
  | c :: r =>
    if c = us then
      match r with
      | 117 :: r' => us :: unesc r'
      | 104 :: r' => hy :: unesc r'
      | 120 :: a :: b :: r' => (unhex a * 16 + unhex b) :: unesc r'
      | _ => []
    else c :: unesc r
termination_by l => l.length

def decode : List Nat → List Nat
  | [] => []
  | c :: r =>
    if c = us then unesc r
    else (c :: r).map dec

theorem hex_roundtrip (c : Nat) (h : c < 256) :
    unhex (hexDigit (c / 16)) * 16 + unhex (hexDigit (c % 16)) = c := by
  have h1 : c / 16 < 16 := by omega
  have h2 : c % 16 < 16 := Nat.mod_lt _ (by omega)
  unfold unhex hexDigit
  split <;> split <;> split <;> split <;> omega

theorem us_not_alnum : isAlnum us = false := by decide

theorem unesc_cons_ne (c : Nat) (r : List Nat) (h : c ≠ us) : unesc (c :: r) = c :: unesc r := by
  rw [unesc.eq_def]; simp [h]

theorem unesc_esc (l : List Nat) (h : ∀ c ∈ l, c < 256) : unesc (esc l) = l := by
  induction l with
  | nil => simp [esc, unesc]
  | cons c r ih =>
    have hc : c < 256 := h c (by simp)
    have hr : ∀ x ∈ r, x < 256 := fun x hx => h x (by simp [hx])
    simp only [esc, escChar]
    by_cases h1 : c = us
    · subst h1; simp [unesc, ih hr]
    · by_cases h2 : c = hy
      · subst h2; simp [unesc, us, hy, ih hr]
      · by_cases h3 : isAlnum c = true
        · simp only [h1, h2, h3, ite_true, ite_false, List.singleton_append]
          rw [unesc_cons_ne c _ h1, ih hr]
        · simp [h1, h2, h3, unesc, ih hr, hex_roundtrip c hc]

theorem plain_head_ne_us (c : Nat) (r : List Nat) (h : isPlain (c :: r) = true) :
    enc c ≠ us := by
  unfold enc
  simp [isPlain] at h
  have := h.1
  intro heq
  by_cases hc : c = hy
  · subst hc; simp [isAlnum, hy] at this
  · simp [hc] at heq; subst heq; simp [us_not_alnum] at this

theorem dec_enc (x : Nat) (h : x = hy ∨ isAlnum x = true) : dec (enc x) = x := by
  rcases h with rfl | ha
  · decide
  · have : x ≠ us := fun e => by subst e; simp [us_not_alnum] at ha
    have : x ≠ hy := fun e => by subst e; simp [isAlnum, hy] at ha
    simp [enc, dec, *]

theorem decode_varLabel (l : List Nat) (h : ∀ c ∈ l, c < 256) : decode (varLabel l) = l := by
  unfold varLabel
  split
  · rename_i hp
    match l, hp with
    | c :: r, hp =>
      have hne := plain_head_ne_us c r hp
      show decode (List.map enc (c :: r)) = c :: r
      rw [List.map_cons, decode]
      simp only [hne, ite_false]
      rw [← List.map_cons, List.map_map]
      conv => rhs; rw [← List.map_id (c :: r)]
      apply List.map_congr_left
      intro x hx
      simp only [isPlain, Bool.and_eq_true, List.all_eq_true, Bool.or_eq_true, beq_iff_eq] at hp
      exact dec_enc x (hp.2 x hx)
  · simp [decode, unesc_esc l h]

/-- Distinct labels never share a generated TypeQL variable. -/
theorem varLabel_injective (a b : List Nat) (ha : ∀ c ∈ a, c < 256) (hb : ∀ c ∈ b, c < 256)
    (h : varLabel a = varLabel b) : a = b := by
  rw [← decode_varLabel a ha, ← decode_varLabel b hb, h]

/-! The model agrees with the Go test vectors in internal/naming/naming_test.go. -/

def bytes (s : String) : List Nat := s.toList.map Char.toNat

example : varLabel (bytes "first-name") = bytes "first_name" := by decide
example : varLabel (bytes "first_name") = bytes "_first_uname" := by decide
example : varLabel (bytes "a-b_c") = bytes "_a_hb_uc" := by decide
example : varLabel (bytes "-x") = bytes "__hx" := by decide
example : varLabel (bytes "person:name") = bytes "_person_x3aname" := by decide
example : varLabel (bytes "") = bytes "_" := by decide

end VarLabel
