# REST codec-declared middleware — 3 improvement candidates found via reqreply/events design work

> **Status:** Idea only — no code written, no spike run yet. Spun out of
> the ReqReply Codec-Declared Middleware design's Round 15 review
> (that roadmap doc has since shipped and been deleted per its own
> graduation policy; see [D-0003](../design/d-0003-codec-declared-middlewares.md)'s
> own Addendum for the durable record), while tracing REST's REAL
> `checkParamConflicts`/`applyParamDeclarations`/`runMiddlewareHandlersReflect`
> code for the first time as the reference implementation for that
> doc's own decisions. All 3 items below are gaps/asymmetries in
> ALREADY-SHIPPED REST code (`api/rest`, `adapters/nethttp`), confirmed
> via direct code citation, not hypotheses — but none are implemented
> or even fully scoped here. This doc is a landing place for the
> candidates, not a committed plan.
> [← Back to Roadmap](index.md)

## Motivation

While designing `api/reqreply`'s (and `api/events`') own NEW codec-declared
middleware mechanism (see
the D-0003 Addendum referenced above),
several rounds of review required tracing REST's REAL, shipped D-0003
implementation in detail — not just its public signatures — as the
reference model those docs' own decisions are checked against. Doing so
surfaced 3 items where REST's OWN real code has a gap, asymmetry, or
looser behavior than the NEW reqreply/events mechanism now intentionally
adopts. Two of these are genuine **behavioral changes** to already-shipped,
already-callable REST code — NOT safe, purely-additive fixes — so they
need their own compat-risk discussion before any implementation is even
seriously scoped. The third is a small, additive observability gap.

This doc exists to NOT LOSE these findings (confirmed via real code,
easy to forget once the reqreply/events work concludes), while being
explicit that NONE of them are committed work — each needs its own
scoping/decision pass, likely as 3 SEPARATE, INDEPENDENT follow-ups
(they don't need to ship together).

## Candidate 1 — cross-kind namespace strictness in `checkParamConflicts` (BEHAVIORAL CHANGE)

**Confirmed via real code** (`api/rest/middleware.go`'s
`applyParamDeclarations`): header, cookie, and query params ALL feed
into ONE combined `request map[string][]paramContribution`, keyed by
`Name` ONLY (not `(kind, name)`). `checkParamConflicts` then compares
`kind`/`required` for every contribution sharing a name:

```go
func checkParamConflicts(routeLabel string, contributions map[string][]paramContribution) error {
	for name, list := range contributions {
		first := list[0]
		for _, c := range list[1:] {
			if c.kind != first.kind || c.required != first.required {
				return ConflictingParamContributionError{...}
			}
		}
	}
	return nil
}
```

**Practical consequence**: a route declaring a HEADER named "X" (e.g.
via `HeaderParam{Name: "X"}`) and, separately, a QUERY param ALSO named
"X" (`QueryParam{Name: "X"}`) would be REJECTED at `Register`/`ValidateRoute`
time with `ConflictingParamContributionError` — even though a header
and a query param are semantically unrelated values from genuinely
different parts of an HTTP request. This is REST's real, shipped,
CURRENT behavior today.

**Why this surfaced now**: the (now-deleted) ReqReply Codec-Declared Middleware roadmap doc's own
Round 15 faced an analogous question — should reqreply's NEW topic-var
vs. property axes share ONE namespace (mirroring REST) or be
INDEPENDENT? The user chose INDEPENDENT for reqreply/events, with a
stronger justification (topic vars and properties come from genuinely
different WIRE LOCATIONS — a topic template string vs. out-of-band
message metadata — not just different parts of one HTTP request, which
is REST's own case). This makes REST's existing cross-kind strictness
worth re-examining: is it actually correct/desired, or is it a
previously-unexamined side effect of using one shared map for
implementation convenience?

**Candidate change**: split REST's single `request`/`response`
contribution maps into THREE per-kind maps each (header/cookie/query
for request; response-header/response-cookie for response), so a
header and a query param sharing a name never conflict with each other
— only same-kind contributions (two headers, two queries, etc.) would
still be checked.

**Why this is a BEHAVIORAL CHANGE, not a safe additive fix**:
- Any EXISTING route relying on today's cross-kind rejection (e.g. a
  test or a real caller that intentionally uses this to CATCH an
  accidental header/query name collision) would silently stop being
  protected — the opposite direction (an existing caller RELYING ON
  the rejection happening) is the compat risk, not "existing code
  breaks," since relaxing a validation can never break a program that
  compiled before.
- Conversely, if any EXISTING caller currently has (unnoticed) a header
  and a query param sharing a name and is CURRENTLY blocked by this
  check from registering that route at all — relaxing it would
  suddenly ALLOW something that previously errored, a genuine behavior
  change in the other direction too.
- Needs its own audit: grep the codebase's own examples/tests for any
  route relying on (or accidentally triggering) today's cross-kind
  check before deciding.

**Open question**: should this be a strict "always independent" change
(matching reqreply/events' new design), or should it be a NEW opt-in
(e.g. a `RouteOpt` toggling namespace scope), preserving today's default
behavior for existing callers? Not resolved here.

## Candidate 2 — no codec/schema comparison in `checkParamConflicts` (BEHAVIORAL CHANGE)

**Confirmed via real code**: REST's `paramContribution` struct
(`api/rest/middleware.go`) is:

```go
type paramContribution struct {
	source   string
	kind     string
	required bool
}
```

No `Codec` field at all. `checkParamConflicts`'s comparison is
`c.kind != first.kind || c.required != first.required` — codecs are
NEVER compared. Two contributions for the SAME name with the SAME
`kind`/`required` but WILDLY DIFFERENT codecs (e.g. one validates as a
UUID, the other as a free-form string) currently pass silently, with
NO error — REST's real, shipped, CURRENT behavior.

**Why this surfaced now**: the (now-deleted) ReqReply Codec-Declared Middleware roadmap doc's own
Round 15 found and corrected an OVERSTATEMENT in its own earlier prose,
which incorrectly claimed REST's real precedent already compares
"different codecs." Once corrected, the user chose to have reqreply's/
events' OWN new mechanism deliberately go BEYOND REST's real precedent —
adding a `Codec.Schema`-based comparison (via `reflect.DeepEqual`,
treating nil-vs-non-nil as a mismatch) for genuinely new code with no
existing callers to break.

**Candidate change**: retrofit the SAME Schema-based comparison into
REST's own `checkParamConflicts`, for consistency across all three APIs
(REST/events/reqreply) once reqreply/events ship their stricter version —
so the "same mechanism, same behavior across all 3 APIs" promise this
codebase's own design philosophy emphasizes actually holds for THIS
check too.

**Why this is a BEHAVIORAL CHANGE, not a safe additive fix**: any
EXISTING REST route with two same-name/same-kind/same-required
contributions that happen to have DIFFERING codec schemas (however that
arose — possibly an existing, unnoticed inconsistency) would START
FAILING to register at all, a hard compile-time-adjacent regression for
real running code, not just a test. This is the RISKIER of the two
namespace/codec changes — a schema mismatch is far more likely to
exist "by accident" in real code than a deliberate cross-kind name
collision (Candidate 1) would be.

**Open question**: should this be opt-in (a new `RouteOpt`/builder flag),
gradual (a warning/observer event before becoming a hard error in a
later release), or not pursued at all for REST (accepting the
inconsistency between REST's looser precedent and reqreply/events' new
stricter one as a permanent, acceptable divergence)? Not resolved here.

## Candidate 3 — missing Observer call on the middleware output-encode path (additive, lower-risk)

**Confirmed via real code** (`adapters/nethttp/serve.go`'s middleware
`EncodeOut` composition loop, immediately after the route's own handler
has produced its `Resp`):

```go
for i, h := range middlewareHandlers {
    mwHeaders, mwCookies, encErr := h.EncodeOut(middlewareOuts[i])
    if encErr != nil {
        errFn(sw, r, http.StatusInternalServerError, encErr)
        return
    }
    // ...
}
```

No `stats.ReportErrors` call anywhere in this block — confirmed via
direct inspection, not just absence-of-grep-match. Compare to the SAME
file's `runMiddlewareHandlersReflect` (the pre-handler dispatch path),
which DOES call `stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx},
"middleware:in", err)` and `stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx},
"middleware:fn", fnErr)` for its own two failure classes (D5, confirmed
in the (now-deleted) ReqReply Codec-Declared Middleware roadmap doc's
own Round 7 review (see D-0003's own Addendum); `DiagnosticObserver` was
later moved from a private `adapters/nethttp`/`adapters/chi` type into
the exported `rest.DiagnosticObserver` (see
[Feature: Observer Pattern](../features/observer.md#apirest--rests-diagnostics-ferry-mechanism-consolidated-into-the-api-layer)) —
same mechanism, new location, both `adapter.go` copies deleted. The
OUTPUT-encode failure path — a THIRD, distinct failure class — has NO
equivalent observability call at all.

**Candidate change**: add `stats.ReportErrors(obs, "middleware:out", err)`
(or similar location string) alongside the EXISTING `"middleware:in"`/
`"middleware:fn"` calls, for full three-way parity.

**Why this IS a safe, additive fix (unlike Candidates 1/2)**: adding an
observer call changes NO validation/dispatch behavior — no existing
request that succeeds or fails today would succeed or fail differently.
Purely additive observability. Lowest-risk item in this doc, though
still not implemented here (this doc is investigation/planning only).

## Out of scope for this doc

- Actually implementing any of the 3 candidates — each needs its own
  scoping pass (especially Candidates 1/2, which need explicit compat-
  risk sign-off given they change already-shipped, already-callable
  REST behavior).
- Auditing `api/events`' OWN equivalent conflict-detection code for the
  SAME 2 behavioral-change candidates — `api/events` has its OWN
  `checkEventsParamConflicts` (per
  the (now-deleted) ReqReply Codec-Declared Middleware roadmap doc's
  Phase 0 (see D-0003's own Addendum), which will presumably launch with the STRICTER, reqreply-
  aligned behavior from day one (no existing callers yet) — but
  whether events' pre-Phase-0-shipped topic-only conflict detection
  (if any exists today) has the SAME cross-kind-strictness question is
  unconfirmed, not investigated in this pass.
- Any DEEPER audit of whether `checkImplementationsDeclared`/security-
  contribution conflict detection (a SEPARATE, adjacent check in the
  same file) has similar gaps — out of scope, not investigated here.

## Next steps (not started)

1. Decide, independently for each candidate, whether to pursue it at
   all (all 3 are OPTIONAL improvements, not confirmed bugs — REST's
   current behavior is a valid, if inconsistent-with-the-newer-doc,
   design choice).
2. For Candidates 1/2 specifically: audit existing REST routes/tests/
   examples for any reliance on today's behavior before scoping a
   change, and decide opt-in vs. default-on vs. not-pursued.
3. For Candidate 3: straightforward to scope and implement independently
   of 1/2, whenever picked up — no dependency between the 3 items.
