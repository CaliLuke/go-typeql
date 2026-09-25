---------------------------- MODULE TxHandle ----------------------------
(***************************************************************************)
(* Model of native-handle ownership for one driver.Transaction             *)
(* (driver/transaction.go, driver/driver.go).                              *)
(*                                                                         *)
(* Actors: the user goroutine (a bounded, nondeterministic sequence of     *)
(* lifecycle calls, including a streaming query whose callback makes        *)
(* nested calls), one background goroutine per QueryWithContext /          *)
(* QueryEachWithContext call, the async close workers, the GC (weak pointer*)
(* expiry + finalizer), and Driver.Close.                                  *)
(*                                                                         *)
(* t.mu is modelled as an explicit lock because a stream releases it in    *)
(* the middle of an operation. stateMu, lease.mu and w.mu sections are     *)
(* single atomic steps.                                                    *)
(***************************************************************************)
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS NOps,          \* user operations
          Ops,           \* subset of operation names the user may pick
          CbOps,         \* nested operations a stream callback may pick
          WithDriverClose

User    == 1
Workers == {2, 5}                \* several goroutines drain one close queue
Closer  == 3
GC      == 4
BgIds   == 10..(10 + NOps)
Bg(k)   == 10 + k

(* --algorithm TxHandle {
variables
    \* Transaction fields
    ptr = TRUE, abandoned = FALSE, ctxCalls = 0,
    streaming = FALSE, closeReq = FALSE, closeCbs = 0,
    opened = TRUE, mu = 0,
    lease = TRUE,                 \* lease.ptr non-nil
    slotOnceDone = FALSE,
    \* registry / GC
    userRef = TRUE, weakNil = FALSE, closerRef = FALSE, finalized = FALSE,
    \* close worker
    \* close workers: held[w] is the job a worker took (-1 = none); a job's
    \* release closure captures the Transaction, so a held job keeps it reachable
    queue = <<>>, wClosed = FALSE, wExited = {},
    held = [w \in Workers |-> -1],
    \* driver
    closing = FALSE, driverFreed = FALSE,
    \* background goroutines: "idle" | "query" | "stream" | "done"
    bg = [k \in BgIds |-> "idle"],
    \* ghosts
    handle = "live", ffiBusy = 0, slotReleases = 0, onDoneOwed = 0;

define {
    BgAlive == \E k \in BgIds : bg[k] \in {"query", "stream"}
    Reachable == userRef \/ closerRef \/ BgAlive \/ queue # <<>>
                 \/ \E w \in Workers : held[w] >= 0
    WDone == wExited = Workers

    NoDoubleSlotRelease == slotReleases <= 1
    OnDoneNotOverpaid   == onDoneOwed >= 0
    \* Driver.Close must not free the Rust driver while a handle is live.
    DriverOutlivesHandle == driverFreed => handle = "freed"
    \* t.mu is a mutex: at most one FFI call on the handle at a time.
    SingleThreaded == ffiBusy <= 1

    Clean == handle = "freed" /\ slotReleases = 1 /\ onDoneOwed = 0
}

macro lock()   { await mu = 0; mu := self; }
macro unlock() { assert mu = self; mu := 0; }
macro ffi()    { assert handle = "live" /\ ~driverFreed; }
macro free()   { assert handle = "live" /\ ~driverFreed; handle := "freed"; }
macro relSlot() { slotReleases := slotReleases + 1; }
macro releaseSlotOnce() {
    if (~slotOnceDone) { slotOnceDone := TRUE; relSlot(); }
}

\* closeAsyncJob / finalizer / finishContextCall tail: enqueue or drop inline.
\* `owed` = number of onDone callbacks that ride on this job.
procedure CloseJob(owed = 0)
{
cj_enq:
    either {
        await ~wClosed;          \* queue accepted the job
        queue := Append(queue, owed);
        return;
    } or {
        skip;                    \* closed worker or full queue: drop inline
    };
cj_drop:
    free();
    releaseSlotOnce();
    onDoneOwed := onDoneOwed - owed;
    return;
}

\* detachCloseJobLocked: caller holds mu. Result in detached.
\* (claimNativeHandle + t.ptr = nil + markClosedLocked)
procedure CloseAsyncP(withCb = FALSE)
variables got = FALSE;
{
ca_abandon:
    if (withCb) { onDoneOwed := onDoneOwed + 1; };
ca_check:
    if (abandoned) {
        if (withCb) { onDoneOwed := onDoneOwed - 1; };
        return;
    };
ca_lock:
    lock();
ca_body:
    if (streaming) {
        closeReq := TRUE;
        if (withCb) { closeCbs := closeCbs + 1; };
        unlock();
        return;
    } else if (ptr) {
        got := lease; lease := FALSE; ptr := FALSE; opened := FALSE;
        unlock();
    } else {
        unlock();
    };
ca_job:
    if (got) {
        call CloseJob(IF withCb THEN 1 ELSE 0);
        return;
    } else {
        if (withCb) { onDoneOwed := onDoneOwed - 1; };
        return;
    }
}

procedure CommitP()
{
cm_check:
    if (abandoned) { return; };
cm_lock:
    lock();
cm_body:
    if (streaming \/ ~ptr) { unlock(); return; };
cm_ffi:
    free();                           \* commit consumes the handle
    lease := FALSE; ptr := FALSE; opened := FALSE;
    releaseSlotOnce();
    unlock();
    return;
}

procedure RollbackP()
{
rb_check:
    if (abandoned) { return; };
rb_lock:
    lock();
rb_body:
    if (streaming \/ ~ptr) { unlock(); return; };
rb_ffi:
    either {
        ffi();                         \* rollback failed: handle kept
        unlock();
        return;
    } or {
        free();                        \* rollback + drop
        lease := FALSE; ptr := FALSE; opened := FALSE;
        releaseSlotOnce();
        unlock();
        return;
    }
}

procedure CloseCheckedP()
variables cgot = FALSE;
{
cc_check:
    if (abandoned) { return; };
cc_lock:
    lock();
cc_body:
    if (streaming) { unlock(); return; }
    else {
        if (ptr) { cgot := lease; lease := FALSE; ptr := FALSE; opened := FALSE; };
        unlock();
    };
cc_close:
    if (cgot) { free(); releaseSlotOnce(); };
    return;
}

procedure IsOpenP()
{
io_check:
    if (abandoned) { return; };
io_lock:
    lock();
io_body:
    if (ptr /\ ~closeReq) { ffi(); };
    unlock();
    return;
}

\* finishContextCall
procedure FinishCtx()
variables cleanup = FALSE, fgot = FALSE;
{
fc_state:
    ctxCalls := ctxCalls - 1;
    cleanup := abandoned /\ ctxCalls = 0;
fc_lock:
    if (~cleanup) { return; } else { lock(); };
fc_body:
    if (streaming) { unlock(); return; }
    else {
        if (ptr) { fgot := lease; lease := FALSE; ptr := FALSE; opened := FALSE; };
        unlock();
    };
fc_job:
    if (fgot) { call CloseJob(0); return; } else { return; };
}

\* queryDecoded
procedure QueryP()
{
q_check:
    if (abandoned) { return; };
q_lock:
    lock();
q_body:
    if (streaming \/ ~ptr) { unlock(); return; };
q_ffi:
    ffi();
    ffiBusy := ffiBusy + 1;
q_ffi_end:
    ffiBusy := ffiBusy - 1;
    unlock();
    return;
}

\* queryEachConfigured + consumeStreamRow + finishStream.
\* isUser: the callback is user code that may make one nested call.
procedure StreamP(isUser = FALSE)
variables chunks = 0, sgot = FALSE, cbs = 0, cbop = "none";
{
s_check:
    if (abandoned) { return; };
s_lock:
    lock();
s_body:
    if (streaming \/ ~ptr) { unlock(); return; } else { streaming := TRUE; };
s_open:
    ffi();                               \* stream_open
s_next:
    while (chunks < 2) {
        chunks := chunks + 1;
        ffi();                           \* stream_next
s_row:
        if (closeReq) { goto s_drop; } else { unlock(); };  \* callback runs without mu
s_cb:
        if (isUser) {
            with (x \in CbOps) { cbop := x; };
        } else {
            cbop := "none";
        };
s_cb_run:
        if (cbop = "close") {
            call CloseAsyncP(TRUE);
        } else if (cbop = "commit") {
            call CommitP();
        } else if (cbop = "isopen") {
            call IsOpenP();
        } else if (cbop = "qctx") {
            call NestedCtx();
        };
s_relock:
        lock();
        if (closeReq) { goto s_drop; }
        else { either { skip } or { goto s_drop; }; };  \* callback error / done
    };
s_drop:
    ffi();                               \* typedb_query_stream_drop
s_finish:
    streaming := FALSE;
    if (~closeReq /\ ~abandoned) { unlock(); return; }
    else {
        cbs := closeCbs; closeCbs := 0; closeReq := FALSE;
        if (ptr) { sgot := lease; lease := FALSE; ptr := FALSE; opened := FALSE; };
        unlock();
    };
s_job:
    if (sgot) {
        call CloseJob(cbs);
        return;
    } else {
        onDoneOwed := onDoneOwed - cbs;
        return;
    }
}

\* A QueryWithContext started from inside a stream callback: its background
\* goroutine is always Bg(0).
procedure NestedCtx()
{
n_begin:
    if (abandoned \/ bg[Bg(0)] # "idle") { return; }
    else { ctxCalls := ctxCalls + 1; bg[Bg(0)] := "query"; };
n_wait:
    either { await bg[Bg(0)] = "done"; return; }
    or { if (ctxCalls > 0) { abandoned := TRUE; }; return; };
}

fair process (UserP \in {User})
variables i = 0, op = "none";
{
u_loop:
    while (i < NOps) {
        i := i + 1;
        with (o \in Ops) { op := o; };
u_dispatch:
        if (op = "qctx" \/ op = "qctxstream") {
            if (~abandoned) {
                ctxCalls := ctxCalls + 1;
                bg[Bg(i)] := IF op = "qctx" THEN "query" ELSE "stream";
u_wait:
                either { await bg[Bg(i)] = "done"; }
                or { if (ctxCalls > 0) { abandoned := TRUE; }; };  \* ctx cancelled
            };
        } else if (op = "stream") {
            call StreamP(TRUE);
        } else if (op = "commit") {
            call CommitP();
        } else if (op = "rollback") {
            call RollbackP();
        } else if (op = "close") {
            call CloseAsyncP(TRUE);
        } else if (op = "closechecked") {
            call CloseCheckedP();
        } else if (op = "isopen") {
            call IsOpenP();
        };
    };
u_exit:
    userRef := FALSE;          \* goroutine returns; the Transaction may leak
}

fair process (BgP \in BgIds)
{
b_start:
    await bg[self] \in {"query", "stream"};
    if (bg[self] = "query") {
        call QueryP();
    } else {
        call StreamP(FALSE);
    };
b_finish:
    call FinishCtx();
b_done:
    bg[self] := "done";
}

fair process (WorkerP \in Workers)
{
w_loop:
    while (TRUE) {
        \* range w.jobs: the channel receive takes one job atomically
        await queue # <<>> \/ wClosed;
        if (queue = <<>>) { wExited := wExited \cup {self}; goto w_end; }
        else { held[self] := Head(queue); queue := Tail(queue); };
w_run:
        free();                                  \* typedb_transaction_close
        releaseSlotOnce();
        onDoneOwed := onDoneOwed - held[self];
        held[self] := -1;
    };
w_end:
    skip;
}

\* GC: once the Transaction is unreachable the weak pointer reads nil,
\* then the finalizer eventually runs (it resurrects t; weakNil stays TRUE).
fair process (GCP \in {GC})
variables ggot = FALSE;
{
g_expire:
    await ~Reachable;
    weakNil := TRUE;
g_final:
    if (abandoned) { goto g_end; };
g_lock:
    lock();
g_body:
    if (streaming) { unlock(); goto g_end; }
    else {
        if (ptr) { ggot := lease; lease := FALSE; ptr := FALSE; opened := FALSE; };
        unlock();
    };
g_job:
    if (ggot) { call CloseJob(0); };
g_end:
    finalized := TRUE;
}

fair process (CloserP \in {Closer})
variables lgot = FALSE;
{
d_start:
    if (~WithDriverClose) { goto d_end; } else { closing := TRUE; };
d_open:
    \* openTransactions: strong ref if the weak pointer is still live.
    if (~weakNil /\ opened) { closerRef := TRUE; } else { goto d_orphans; };
d_close_tx:
    call CloseAsyncP(FALSE);
d_drop_ref:
    closerRef := FALSE;
d_orphans:
    \* orphanedCloseJobs: claim leases whose weak pointer is nil.
    if (weakNil /\ lease) { lease := FALSE; lgot := TRUE; };
d_run_orphan:
    if (lgot) { free(); relSlot(); };     \* lease.release is the raw func
d_worker:
    wClosed := TRUE;
d_worker_wait:
    await WDone;                          \* wg.Wait, then close(w.done)
d_wg:
    await slotReleases >= 1;              \* nativeWG.Wait
d_free:
    driverFreed := TRUE;
d_end:
    skip;
}
} *)
\* BEGIN TRANSLATION
VARIABLES ptr, abandoned, ctxCalls, streaming, closeReq, closeCbs, opened, mu, 
          lease, slotOnceDone, userRef, weakNil, closerRef, finalized, queue, 
          wClosed, wExited, held, closing, driverFreed, bg, handle, ffiBusy, 
          slotReleases, onDoneOwed, pc, stack

(* define statement *)
BgAlive == \E k \in BgIds : bg[k] \in {"query", "stream"}
Reachable == userRef \/ closerRef \/ BgAlive \/ queue # <<>>
             \/ \E w \in Workers : held[w] >= 0
WDone == wExited = Workers

NoDoubleSlotRelease == slotReleases <= 1
OnDoneNotOverpaid   == onDoneOwed >= 0

DriverOutlivesHandle == driverFreed => handle = "freed"

SingleThreaded == ffiBusy <= 1

Clean == handle = "freed" /\ slotReleases = 1 /\ onDoneOwed = 0

VARIABLES owed, withCb, got, cgot, cleanup, fgot, isUser, chunks, sgot, cbs, 
          cbop, i, op, ggot, lgot

vars == << ptr, abandoned, ctxCalls, streaming, closeReq, closeCbs, opened, 
           mu, lease, slotOnceDone, userRef, weakNil, closerRef, finalized, 
           queue, wClosed, wExited, held, closing, driverFreed, bg, handle, 
           ffiBusy, slotReleases, onDoneOwed, pc, stack, owed, withCb, got, 
           cgot, cleanup, fgot, isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
           lgot >>

ProcSet == ({User}) \cup (BgIds) \cup (Workers) \cup ({GC}) \cup ({Closer})

Init == (* Global variables *)
        /\ ptr = TRUE
        /\ abandoned = FALSE
        /\ ctxCalls = 0
        /\ streaming = FALSE
        /\ closeReq = FALSE
        /\ closeCbs = 0
        /\ opened = TRUE
        /\ mu = 0
        /\ lease = TRUE
        /\ slotOnceDone = FALSE
        /\ userRef = TRUE
        /\ weakNil = FALSE
        /\ closerRef = FALSE
        /\ finalized = FALSE
        /\ queue = <<>>
        /\ wClosed = FALSE
        /\ wExited = {}
        /\ held = [w \in Workers |-> -1]
        /\ closing = FALSE
        /\ driverFreed = FALSE
        /\ bg = [k \in BgIds |-> "idle"]
        /\ handle = "live"
        /\ ffiBusy = 0
        /\ slotReleases = 0
        /\ onDoneOwed = 0
        (* Procedure CloseJob *)
        /\ owed = [ self \in ProcSet |-> 0]
        (* Procedure CloseAsyncP *)
        /\ withCb = [ self \in ProcSet |-> FALSE]
        /\ got = [ self \in ProcSet |-> FALSE]
        (* Procedure CloseCheckedP *)
        /\ cgot = [ self \in ProcSet |-> FALSE]
        (* Procedure FinishCtx *)
        /\ cleanup = [ self \in ProcSet |-> FALSE]
        /\ fgot = [ self \in ProcSet |-> FALSE]
        (* Procedure StreamP *)
        /\ isUser = [ self \in ProcSet |-> FALSE]
        /\ chunks = [ self \in ProcSet |-> 0]
        /\ sgot = [ self \in ProcSet |-> FALSE]
        /\ cbs = [ self \in ProcSet |-> 0]
        /\ cbop = [ self \in ProcSet |-> "none"]
        (* Process UserP *)
        /\ i = [self \in {User} |-> 0]
        /\ op = [self \in {User} |-> "none"]
        (* Process GCP *)
        /\ ggot = [self \in {GC} |-> FALSE]
        (* Process CloserP *)
        /\ lgot = [self \in {Closer} |-> FALSE]
        /\ stack = [self \in ProcSet |-> << >>]
        /\ pc = [self \in ProcSet |-> CASE self \in {User} -> "u_loop"
                                        [] self \in BgIds -> "b_start"
                                        [] self \in Workers -> "w_loop"
                                        [] self \in {GC} -> "g_expire"
                                        [] self \in {Closer} -> "d_start"]

cj_enq(self) == /\ pc[self] = "cj_enq"
                /\ \/ /\ ~wClosed
                      /\ queue' = Append(queue, owed[self])
                      /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                      /\ owed' = [owed EXCEPT ![self] = Head(stack[self]).owed]
                      /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                   \/ /\ TRUE
                      /\ pc' = [pc EXCEPT ![self] = "cj_drop"]
                      /\ UNCHANGED <<queue, stack, owed>>
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, 
                                wClosed, wExited, held, closing, driverFreed, 
                                bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

cj_drop(self) == /\ pc[self] = "cj_drop"
                 /\ Assert(handle = "live" /\ ~driverFreed, 
                           "Failure of assertion at line 71, column 18 of macro called at line 90, column 5.")
                 /\ handle' = "freed"
                 /\ IF ~slotOnceDone
                       THEN /\ slotOnceDone' = TRUE
                            /\ slotReleases' = slotReleases + 1
                       ELSE /\ TRUE
                            /\ UNCHANGED << slotOnceDone, slotReleases >>
                 /\ onDoneOwed' = onDoneOwed - owed[self]
                 /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                 /\ owed' = [owed EXCEPT ![self] = Head(stack[self]).owed]
                 /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, mu, lease, userRef, weakNil, 
                                 closerRef, finalized, queue, wClosed, wExited, 
                                 held, closing, driverFreed, bg, ffiBusy, 
                                 withCb, got, cgot, cleanup, fgot, isUser, 
                                 chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

CloseJob(self) == cj_enq(self) \/ cj_drop(self)

ca_abandon(self) == /\ pc[self] = "ca_abandon"
                    /\ IF withCb[self]
                          THEN /\ onDoneOwed' = onDoneOwed + 1
                          ELSE /\ TRUE
                               /\ UNCHANGED onDoneOwed
                    /\ pc' = [pc EXCEPT ![self] = "ca_check"]
                    /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                    closeReq, closeCbs, opened, mu, lease, 
                                    slotOnceDone, userRef, weakNil, closerRef, 
                                    finalized, queue, wClosed, wExited, held, 
                                    closing, driverFreed, bg, handle, ffiBusy, 
                                    slotReleases, stack, owed, withCb, got, 
                                    cgot, cleanup, fgot, isUser, chunks, sgot, 
                                    cbs, cbop, i, op, ggot, lgot >>

ca_check(self) == /\ pc[self] = "ca_check"
                  /\ IF abandoned
                        THEN /\ IF withCb[self]
                                   THEN /\ onDoneOwed' = onDoneOwed - 1
                                   ELSE /\ TRUE
                                        /\ UNCHANGED onDoneOwed
                             /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                             /\ got' = [got EXCEPT ![self] = Head(stack[self]).got]
                             /\ withCb' = [withCb EXCEPT ![self] = Head(stack[self]).withCb]
                             /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                        ELSE /\ pc' = [pc EXCEPT ![self] = "ca_lock"]
                             /\ UNCHANGED << onDoneOwed, stack, withCb, got >>
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, owed, cgot, cleanup, fgot, 
                                  isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                  lgot >>

ca_lock(self) == /\ pc[self] = "ca_lock"
                 /\ mu = 0
                 /\ mu' = self
                 /\ pc' = [pc EXCEPT ![self] = "ca_body"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 stack, owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

ca_body(self) == /\ pc[self] = "ca_body"
                 /\ IF streaming
                       THEN /\ closeReq' = TRUE
                            /\ IF withCb[self]
                                  THEN /\ closeCbs' = closeCbs + 1
                                  ELSE /\ TRUE
                                       /\ UNCHANGED closeCbs
                            /\ Assert(mu = self, 
                                      "Failure of assertion at line 69, column 18 of macro called at line 114, column 9.")
                            /\ mu' = 0
                            /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ got' = [got EXCEPT ![self] = Head(stack[self]).got]
                            /\ withCb' = [withCb EXCEPT ![self] = Head(stack[self]).withCb]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                            /\ UNCHANGED << ptr, opened, lease >>
                       ELSE /\ IF ptr
                                  THEN /\ got' = [got EXCEPT ![self] = lease]
                                       /\ lease' = FALSE
                                       /\ ptr' = FALSE
                                       /\ opened' = FALSE
                                       /\ Assert(mu = self, 
                                                 "Failure of assertion at line 69, column 18 of macro called at line 118, column 9.")
                                       /\ mu' = 0
                                  ELSE /\ Assert(mu = self, 
                                                 "Failure of assertion at line 69, column 18 of macro called at line 120, column 9.")
                                       /\ mu' = 0
                                       /\ UNCHANGED << ptr, opened, lease, got >>
                            /\ pc' = [pc EXCEPT ![self] = "ca_job"]
                            /\ UNCHANGED << closeReq, closeCbs, stack, withCb >>
                 /\ UNCHANGED << abandoned, ctxCalls, streaming, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, cgot, cleanup, fgot, isUser, chunks, 
                                 sgot, cbs, cbop, i, op, ggot, lgot >>

ca_job(self) == /\ pc[self] = "ca_job"
                /\ IF got[self]
                      THEN /\ /\ got' = [got EXCEPT ![self] = Head(stack[self]).got]
                              /\ owed' = [owed EXCEPT ![self] = IF withCb[self] THEN 1 ELSE 0]
                              /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseJob",
                                                                       pc        |->  Head(stack[self]).pc,
                                                                       owed      |->  owed[self] ] >>
                                                                   \o Tail(stack[self])]
                           /\ pc' = [pc EXCEPT ![self] = "cj_enq"]
                           /\ UNCHANGED << onDoneOwed, withCb >>
                      ELSE /\ IF withCb[self]
                                 THEN /\ onDoneOwed' = onDoneOwed - 1
                                 ELSE /\ TRUE
                                      /\ UNCHANGED onDoneOwed
                           /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                           /\ got' = [got EXCEPT ![self] = Head(stack[self]).got]
                           /\ withCb' = [withCb EXCEPT ![self] = Head(stack[self]).withCb]
                           /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                           /\ owed' = owed
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, driverFreed, 
                                bg, handle, ffiBusy, slotReleases, cgot, 
                                cleanup, fgot, isUser, chunks, sgot, cbs, cbop, 
                                i, op, ggot, lgot >>

CloseAsyncP(self) == ca_abandon(self) \/ ca_check(self) \/ ca_lock(self)
                        \/ ca_body(self) \/ ca_job(self)

cm_check(self) == /\ pc[self] = "cm_check"
                  /\ IF abandoned
                        THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                             /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                        ELSE /\ pc' = [pc EXCEPT ![self] = "cm_lock"]
                             /\ stack' = stack
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, owed, withCb, got, 
                                  cgot, cleanup, fgot, isUser, chunks, sgot, 
                                  cbs, cbop, i, op, ggot, lgot >>

cm_lock(self) == /\ pc[self] = "cm_lock"
                 /\ mu = 0
                 /\ mu' = self
                 /\ pc' = [pc EXCEPT ![self] = "cm_body"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 stack, owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

cm_body(self) == /\ pc[self] = "cm_body"
                 /\ IF streaming \/ ~ptr
                       THEN /\ Assert(mu = self, 
                                      "Failure of assertion at line 69, column 18 of macro called at line 139, column 30.")
                            /\ mu' = 0
                            /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                       ELSE /\ pc' = [pc EXCEPT ![self] = "cm_ffi"]
                            /\ UNCHANGED << mu, stack >>
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

cm_ffi(self) == /\ pc[self] = "cm_ffi"
                /\ Assert(handle = "live" /\ ~driverFreed, 
                          "Failure of assertion at line 71, column 18 of macro called at line 141, column 5.")
                /\ handle' = "freed"
                /\ lease' = FALSE
                /\ ptr' = FALSE
                /\ opened' = FALSE
                /\ IF ~slotOnceDone
                      THEN /\ slotOnceDone' = TRUE
                           /\ slotReleases' = slotReleases + 1
                      ELSE /\ TRUE
                           /\ UNCHANGED << slotOnceDone, slotReleases >>
                /\ Assert(mu = self, 
                          "Failure of assertion at line 69, column 18 of macro called at line 144, column 5.")
                /\ mu' = 0
                /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                /\ UNCHANGED << abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, userRef, weakNil, closerRef, 
                                finalized, queue, wClosed, wExited, held, 
                                closing, driverFreed, bg, ffiBusy, onDoneOwed, 
                                owed, withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

CommitP(self) == cm_check(self) \/ cm_lock(self) \/ cm_body(self)
                    \/ cm_ffi(self)

rb_check(self) == /\ pc[self] = "rb_check"
                  /\ IF abandoned
                        THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                             /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                        ELSE /\ pc' = [pc EXCEPT ![self] = "rb_lock"]
                             /\ stack' = stack
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, owed, withCb, got, 
                                  cgot, cleanup, fgot, isUser, chunks, sgot, 
                                  cbs, cbop, i, op, ggot, lgot >>

rb_lock(self) == /\ pc[self] = "rb_lock"
                 /\ mu = 0
                 /\ mu' = self
                 /\ pc' = [pc EXCEPT ![self] = "rb_body"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 stack, owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

rb_body(self) == /\ pc[self] = "rb_body"
                 /\ IF streaming \/ ~ptr
                       THEN /\ Assert(mu = self, 
                                      "Failure of assertion at line 69, column 18 of macro called at line 155, column 30.")
                            /\ mu' = 0
                            /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                       ELSE /\ pc' = [pc EXCEPT ![self] = "rb_ffi"]
                            /\ UNCHANGED << mu, stack >>
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

rb_ffi(self) == /\ pc[self] = "rb_ffi"
                /\ \/ /\ Assert(handle = "live" /\ ~driverFreed, 
                                "Failure of assertion at line 70, column 18 of macro called at line 158, column 9.")
                      /\ Assert(mu = self, 
                                "Failure of assertion at line 69, column 18 of macro called at line 159, column 9.")
                      /\ mu' = 0
                      /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                      /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                      /\ UNCHANGED <<ptr, opened, lease, slotOnceDone, handle, slotReleases>>
                   \/ /\ Assert(handle = "live" /\ ~driverFreed, 
                                "Failure of assertion at line 71, column 18 of macro called at line 162, column 9.")
                      /\ handle' = "freed"
                      /\ lease' = FALSE
                      /\ ptr' = FALSE
                      /\ opened' = FALSE
                      /\ IF ~slotOnceDone
                            THEN /\ slotOnceDone' = TRUE
                                 /\ slotReleases' = slotReleases + 1
                            ELSE /\ TRUE
                                 /\ UNCHANGED << slotOnceDone, slotReleases >>
                      /\ Assert(mu = self, 
                                "Failure of assertion at line 69, column 18 of macro called at line 165, column 9.")
                      /\ mu' = 0
                      /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                      /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                /\ UNCHANGED << abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, userRef, weakNil, closerRef, 
                                finalized, queue, wClosed, wExited, held, 
                                closing, driverFreed, bg, ffiBusy, onDoneOwed, 
                                owed, withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

RollbackP(self) == rb_check(self) \/ rb_lock(self) \/ rb_body(self)
                      \/ rb_ffi(self)

cc_check(self) == /\ pc[self] = "cc_check"
                  /\ IF abandoned
                        THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                             /\ cgot' = [cgot EXCEPT ![self] = Head(stack[self]).cgot]
                             /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                        ELSE /\ pc' = [pc EXCEPT ![self] = "cc_lock"]
                             /\ UNCHANGED << stack, cgot >>
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, owed, withCb, got, 
                                  cleanup, fgot, isUser, chunks, sgot, cbs, 
                                  cbop, i, op, ggot, lgot >>

cc_lock(self) == /\ pc[self] = "cc_lock"
                 /\ mu = 0
                 /\ mu' = self
                 /\ pc' = [pc EXCEPT ![self] = "cc_body"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 stack, owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

cc_body(self) == /\ pc[self] = "cc_body"
                 /\ IF streaming
                       THEN /\ Assert(mu = self, 
                                      "Failure of assertion at line 69, column 18 of macro called at line 178, column 22.")
                            /\ mu' = 0
                            /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ cgot' = [cgot EXCEPT ![self] = Head(stack[self]).cgot]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                            /\ UNCHANGED << ptr, opened, lease >>
                       ELSE /\ IF ptr
                                  THEN /\ cgot' = [cgot EXCEPT ![self] = lease]
                                       /\ lease' = FALSE
                                       /\ ptr' = FALSE
                                       /\ opened' = FALSE
                                  ELSE /\ TRUE
                                       /\ UNCHANGED << ptr, opened, lease, 
                                                       cgot >>
                            /\ Assert(mu = self, 
                                      "Failure of assertion at line 69, column 18 of macro called at line 181, column 9.")
                            /\ mu' = 0
                            /\ pc' = [pc EXCEPT ![self] = "cc_close"]
                            /\ stack' = stack
                 /\ UNCHANGED << abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, slotOnceDone, userRef, weakNil, 
                                 closerRef, finalized, queue, wClosed, wExited, 
                                 held, closing, driverFreed, bg, handle, 
                                 ffiBusy, slotReleases, onDoneOwed, owed, 
                                 withCb, got, cleanup, fgot, isUser, chunks, 
                                 sgot, cbs, cbop, i, op, ggot, lgot >>

cc_close(self) == /\ pc[self] = "cc_close"
                  /\ IF cgot[self]
                        THEN /\ Assert(handle = "live" /\ ~driverFreed, 
                                       "Failure of assertion at line 71, column 18 of macro called at line 184, column 17.")
                             /\ handle' = "freed"
                             /\ IF ~slotOnceDone
                                   THEN /\ slotOnceDone' = TRUE
                                        /\ slotReleases' = slotReleases + 1
                                   ELSE /\ TRUE
                                        /\ UNCHANGED << slotOnceDone, 
                                                        slotReleases >>
                        ELSE /\ TRUE
                             /\ UNCHANGED << slotOnceDone, handle, 
                                             slotReleases >>
                  /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                  /\ cgot' = [cgot EXCEPT ![self] = Head(stack[self]).cgot]
                  /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  userRef, weakNil, closerRef, finalized, 
                                  queue, wClosed, wExited, held, closing, 
                                  driverFreed, bg, ffiBusy, onDoneOwed, owed, 
                                  withCb, got, cleanup, fgot, isUser, chunks, 
                                  sgot, cbs, cbop, i, op, ggot, lgot >>

CloseCheckedP(self) == cc_check(self) \/ cc_lock(self) \/ cc_body(self)
                          \/ cc_close(self)

io_check(self) == /\ pc[self] = "io_check"
                  /\ IF abandoned
                        THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                             /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                        ELSE /\ pc' = [pc EXCEPT ![self] = "io_lock"]
                             /\ stack' = stack
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, owed, withCb, got, 
                                  cgot, cleanup, fgot, isUser, chunks, sgot, 
                                  cbs, cbop, i, op, ggot, lgot >>

io_lock(self) == /\ pc[self] = "io_lock"
                 /\ mu = 0
                 /\ mu' = self
                 /\ pc' = [pc EXCEPT ![self] = "io_body"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 stack, owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

io_body(self) == /\ pc[self] = "io_body"
                 /\ IF ptr /\ ~closeReq
                       THEN /\ Assert(handle = "live" /\ ~driverFreed, 
                                      "Failure of assertion at line 70, column 18 of macro called at line 195, column 29.")
                       ELSE /\ TRUE
                 /\ Assert(mu = self, 
                           "Failure of assertion at line 69, column 18 of macro called at line 196, column 5.")
                 /\ mu' = 0
                 /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                 /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

IsOpenP(self) == io_check(self) \/ io_lock(self) \/ io_body(self)

fc_state(self) == /\ pc[self] = "fc_state"
                  /\ ctxCalls' = ctxCalls - 1
                  /\ cleanup' = [cleanup EXCEPT ![self] = abandoned /\ ctxCalls' = 0]
                  /\ pc' = [pc EXCEPT ![self] = "fc_lock"]
                  /\ UNCHANGED << ptr, abandoned, streaming, closeReq, 
                                  closeCbs, opened, mu, lease, slotOnceDone, 
                                  userRef, weakNil, closerRef, finalized, 
                                  queue, wClosed, wExited, held, closing, 
                                  driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, stack, owed, 
                                  withCb, got, cgot, fgot, isUser, chunks, 
                                  sgot, cbs, cbop, i, op, ggot, lgot >>

fc_lock(self) == /\ pc[self] = "fc_lock"
                 /\ IF ~cleanup[self]
                       THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ cleanup' = [cleanup EXCEPT ![self] = Head(stack[self]).cleanup]
                            /\ fgot' = [fgot EXCEPT ![self] = Head(stack[self]).fgot]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                            /\ mu' = mu
                       ELSE /\ mu = 0
                            /\ mu' = self
                            /\ pc' = [pc EXCEPT ![self] = "fc_body"]
                            /\ UNCHANGED << stack, cleanup, fgot >>
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, withCb, got, cgot, isUser, chunks, sgot, 
                                 cbs, cbop, i, op, ggot, lgot >>

fc_body(self) == /\ pc[self] = "fc_body"
                 /\ IF streaming
                       THEN /\ Assert(mu = self, 
                                      "Failure of assertion at line 69, column 18 of macro called at line 210, column 22.")
                            /\ mu' = 0
                            /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ cleanup' = [cleanup EXCEPT ![self] = Head(stack[self]).cleanup]
                            /\ fgot' = [fgot EXCEPT ![self] = Head(stack[self]).fgot]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                            /\ UNCHANGED << ptr, opened, lease >>
                       ELSE /\ IF ptr
                                  THEN /\ fgot' = [fgot EXCEPT ![self] = lease]
                                       /\ lease' = FALSE
                                       /\ ptr' = FALSE
                                       /\ opened' = FALSE
                                  ELSE /\ TRUE
                                       /\ UNCHANGED << ptr, opened, lease, 
                                                       fgot >>
                            /\ Assert(mu = self, 
                                      "Failure of assertion at line 69, column 18 of macro called at line 213, column 9.")
                            /\ mu' = 0
                            /\ pc' = [pc EXCEPT ![self] = "fc_job"]
                            /\ UNCHANGED << stack, cleanup >>
                 /\ UNCHANGED << abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, slotOnceDone, userRef, weakNil, 
                                 closerRef, finalized, queue, wClosed, wExited, 
                                 held, closing, driverFreed, bg, handle, 
                                 ffiBusy, slotReleases, onDoneOwed, owed, 
                                 withCb, got, cgot, isUser, chunks, sgot, cbs, 
                                 cbop, i, op, ggot, lgot >>

fc_job(self) == /\ pc[self] = "fc_job"
                /\ IF fgot[self]
                      THEN /\ /\ cleanup' = [cleanup EXCEPT ![self] = Head(stack[self]).cleanup]
                              /\ fgot' = [fgot EXCEPT ![self] = Head(stack[self]).fgot]
                              /\ owed' = [owed EXCEPT ![self] = 0]
                              /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseJob",
                                                                       pc        |->  Head(stack[self]).pc,
                                                                       owed      |->  owed[self] ] >>
                                                                   \o Tail(stack[self])]
                           /\ pc' = [pc EXCEPT ![self] = "cj_enq"]
                      ELSE /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                           /\ cleanup' = [cleanup EXCEPT ![self] = Head(stack[self]).cleanup]
                           /\ fgot' = [fgot EXCEPT ![self] = Head(stack[self]).fgot]
                           /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                           /\ owed' = owed
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, driverFreed, 
                                bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                withCb, got, cgot, isUser, chunks, sgot, cbs, 
                                cbop, i, op, ggot, lgot >>

FinishCtx(self) == fc_state(self) \/ fc_lock(self) \/ fc_body(self)
                      \/ fc_job(self)

q_check(self) == /\ pc[self] = "q_check"
                 /\ IF abandoned
                       THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                       ELSE /\ pc' = [pc EXCEPT ![self] = "q_lock"]
                            /\ stack' = stack
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, mu, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

q_lock(self) == /\ pc[self] = "q_lock"
                /\ mu = 0
                /\ mu' = self
                /\ pc' = [pc EXCEPT ![self] = "q_body"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, lease, slotOnceDone, userRef, 
                                weakNil, closerRef, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

q_body(self) == /\ pc[self] = "q_body"
                /\ IF streaming \/ ~ptr
                      THEN /\ Assert(mu = self, 
                                     "Failure of assertion at line 69, column 18 of macro called at line 227, column 30.")
                           /\ mu' = 0
                           /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                           /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                      ELSE /\ pc' = [pc EXCEPT ![self] = "q_ffi"]
                           /\ UNCHANGED << mu, stack >>
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, lease, slotOnceDone, userRef, 
                                weakNil, closerRef, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                owed, withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

q_ffi(self) == /\ pc[self] = "q_ffi"
               /\ Assert(handle = "live" /\ ~driverFreed, 
                         "Failure of assertion at line 70, column 18 of macro called at line 229, column 5.")
               /\ ffiBusy' = ffiBusy + 1
               /\ pc' = [pc EXCEPT ![self] = "q_ffi_end"]
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, mu, lease, slotOnceDone, 
                               userRef, weakNil, closerRef, finalized, queue, 
                               wClosed, wExited, held, closing, driverFreed, 
                               bg, handle, slotReleases, onDoneOwed, stack, 
                               owed, withCb, got, cgot, cleanup, fgot, isUser, 
                               chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

q_ffi_end(self) == /\ pc[self] = "q_ffi_end"
                   /\ ffiBusy' = ffiBusy - 1
                   /\ Assert(mu = self, 
                             "Failure of assertion at line 69, column 18 of macro called at line 233, column 5.")
                   /\ mu' = 0
                   /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                   /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                   /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                   closeReq, closeCbs, opened, lease, 
                                   slotOnceDone, userRef, weakNil, closerRef, 
                                   finalized, queue, wClosed, wExited, held, 
                                   closing, driverFreed, bg, handle, 
                                   slotReleases, onDoneOwed, owed, withCb, got, 
                                   cgot, cleanup, fgot, isUser, chunks, sgot, 
                                   cbs, cbop, i, op, ggot, lgot >>

QueryP(self) == q_check(self) \/ q_lock(self) \/ q_body(self)
                   \/ q_ffi(self) \/ q_ffi_end(self)

s_check(self) == /\ pc[self] = "s_check"
                 /\ IF abandoned
                       THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ chunks' = [chunks EXCEPT ![self] = Head(stack[self]).chunks]
                            /\ sgot' = [sgot EXCEPT ![self] = Head(stack[self]).sgot]
                            /\ cbs' = [cbs EXCEPT ![self] = Head(stack[self]).cbs]
                            /\ cbop' = [cbop EXCEPT ![self] = Head(stack[self]).cbop]
                            /\ isUser' = [isUser EXCEPT ![self] = Head(stack[self]).isUser]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                       ELSE /\ pc' = [pc EXCEPT ![self] = "s_lock"]
                            /\ UNCHANGED << stack, isUser, chunks, sgot, cbs, 
                                            cbop >>
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, mu, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, withCb, got, cgot, cleanup, fgot, i, op, 
                                 ggot, lgot >>

s_lock(self) == /\ pc[self] = "s_lock"
                /\ mu = 0
                /\ mu' = self
                /\ pc' = [pc EXCEPT ![self] = "s_body"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, lease, slotOnceDone, userRef, 
                                weakNil, closerRef, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

s_body(self) == /\ pc[self] = "s_body"
                /\ IF streaming \/ ~ptr
                      THEN /\ Assert(mu = self, 
                                     "Failure of assertion at line 69, column 18 of macro called at line 247, column 30.")
                           /\ mu' = 0
                           /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                           /\ chunks' = [chunks EXCEPT ![self] = Head(stack[self]).chunks]
                           /\ sgot' = [sgot EXCEPT ![self] = Head(stack[self]).sgot]
                           /\ cbs' = [cbs EXCEPT ![self] = Head(stack[self]).cbs]
                           /\ cbop' = [cbop EXCEPT ![self] = Head(stack[self]).cbop]
                           /\ isUser' = [isUser EXCEPT ![self] = Head(stack[self]).isUser]
                           /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                           /\ UNCHANGED streaming
                      ELSE /\ streaming' = TRUE
                           /\ pc' = [pc EXCEPT ![self] = "s_open"]
                           /\ UNCHANGED << mu, stack, isUser, chunks, sgot, 
                                           cbs, cbop >>
                /\ UNCHANGED << ptr, abandoned, ctxCalls, closeReq, closeCbs, 
                                opened, lease, slotOnceDone, userRef, weakNil, 
                                closerRef, finalized, queue, wClosed, wExited, 
                                held, closing, driverFreed, bg, handle, 
                                ffiBusy, slotReleases, onDoneOwed, owed, 
                                withCb, got, cgot, cleanup, fgot, i, op, ggot, 
                                lgot >>

s_open(self) == /\ pc[self] = "s_open"
                /\ Assert(handle = "live" /\ ~driverFreed, 
                          "Failure of assertion at line 70, column 18 of macro called at line 249, column 5.")
                /\ pc' = [pc EXCEPT ![self] = "s_next"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, driverFreed, 
                                bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

s_next(self) == /\ pc[self] = "s_next"
                /\ IF chunks[self] < 2
                      THEN /\ chunks' = [chunks EXCEPT ![self] = chunks[self] + 1]
                           /\ Assert(handle = "live" /\ ~driverFreed, 
                                     "Failure of assertion at line 70, column 18 of macro called at line 253, column 9.")
                           /\ pc' = [pc EXCEPT ![self] = "s_row"]
                      ELSE /\ pc' = [pc EXCEPT ![self] = "s_drop"]
                           /\ UNCHANGED chunks
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, driverFreed, 
                                bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, sgot, cbs, cbop, i, op, ggot, lgot >>

s_row(self) == /\ pc[self] = "s_row"
               /\ IF closeReq
                     THEN /\ pc' = [pc EXCEPT ![self] = "s_drop"]
                          /\ mu' = mu
                     ELSE /\ Assert(mu = self, 
                                    "Failure of assertion at line 69, column 18 of macro called at line 255, column 47.")
                          /\ mu' = 0
                          /\ pc' = [pc EXCEPT ![self] = "s_cb"]
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, lease, slotOnceDone, userRef, 
                               weakNil, closerRef, finalized, queue, wClosed, 
                               wExited, held, closing, driverFreed, bg, handle, 
                               ffiBusy, slotReleases, onDoneOwed, stack, owed, 
                               withCb, got, cgot, cleanup, fgot, isUser, 
                               chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

s_cb(self) == /\ pc[self] = "s_cb"
              /\ IF isUser[self]
                    THEN /\ \E x \in CbOps:
                              cbop' = [cbop EXCEPT ![self] = x]
                    ELSE /\ cbop' = [cbop EXCEPT ![self] = "none"]
              /\ pc' = [pc EXCEPT ![self] = "s_cb_run"]
              /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                              closeCbs, opened, mu, lease, slotOnceDone, 
                              userRef, weakNil, closerRef, finalized, queue, 
                              wClosed, wExited, held, closing, driverFreed, bg, 
                              handle, ffiBusy, slotReleases, onDoneOwed, stack, 
                              owed, withCb, got, cgot, cleanup, fgot, isUser, 
                              chunks, sgot, cbs, i, op, ggot, lgot >>

s_cb_run(self) == /\ pc[self] = "s_cb_run"
                  /\ IF cbop[self] = "close"
                        THEN /\ /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseAsyncP",
                                                                         pc        |->  "s_relock",
                                                                         got       |->  got[self],
                                                                         withCb    |->  withCb[self] ] >>
                                                                     \o stack[self]]
                                /\ withCb' = [withCb EXCEPT ![self] = TRUE]
                             /\ got' = [got EXCEPT ![self] = FALSE]
                             /\ pc' = [pc EXCEPT ![self] = "ca_abandon"]
                        ELSE /\ IF cbop[self] = "commit"
                                   THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CommitP",
                                                                                 pc        |->  "s_relock" ] >>
                                                                             \o stack[self]]
                                        /\ pc' = [pc EXCEPT ![self] = "cm_check"]
                                   ELSE /\ IF cbop[self] = "isopen"
                                              THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "IsOpenP",
                                                                                            pc        |->  "s_relock" ] >>
                                                                                        \o stack[self]]
                                                   /\ pc' = [pc EXCEPT ![self] = "io_check"]
                                              ELSE /\ IF cbop[self] = "qctx"
                                                         THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "NestedCtx",
                                                                                                       pc        |->  "s_relock" ] >>
                                                                                                   \o stack[self]]
                                                              /\ pc' = [pc EXCEPT ![self] = "n_begin"]
                                                         ELSE /\ pc' = [pc EXCEPT ![self] = "s_relock"]
                                                              /\ stack' = stack
                             /\ UNCHANGED << withCb, got >>
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, owed, cgot, 
                                  cleanup, fgot, isUser, chunks, sgot, cbs, 
                                  cbop, i, op, ggot, lgot >>

s_relock(self) == /\ pc[self] = "s_relock"
                  /\ mu = 0
                  /\ mu' = self
                  /\ IF closeReq
                        THEN /\ pc' = [pc EXCEPT ![self] = "s_drop"]
                        ELSE /\ \/ /\ TRUE
                                   /\ pc' = [pc EXCEPT ![self] = "s_next"]
                                \/ /\ pc' = [pc EXCEPT ![self] = "s_drop"]
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, stack, owed, 
                                  withCb, got, cgot, cleanup, fgot, isUser, 
                                  chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

s_drop(self) == /\ pc[self] = "s_drop"
                /\ Assert(handle = "live" /\ ~driverFreed, 
                          "Failure of assertion at line 70, column 18 of macro called at line 278, column 5.")
                /\ pc' = [pc EXCEPT ![self] = "s_finish"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, driverFreed, 
                                bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

s_finish(self) == /\ pc[self] = "s_finish"
                  /\ streaming' = FALSE
                  /\ IF ~closeReq /\ ~abandoned
                        THEN /\ Assert(mu = self, 
                                       "Failure of assertion at line 69, column 18 of macro called at line 281, column 36.")
                             /\ mu' = 0
                             /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                             /\ chunks' = [chunks EXCEPT ![self] = Head(stack[self]).chunks]
                             /\ sgot' = [sgot EXCEPT ![self] = Head(stack[self]).sgot]
                             /\ cbs' = [cbs EXCEPT ![self] = Head(stack[self]).cbs]
                             /\ cbop' = [cbop EXCEPT ![self] = Head(stack[self]).cbop]
                             /\ isUser' = [isUser EXCEPT ![self] = Head(stack[self]).isUser]
                             /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                             /\ UNCHANGED << ptr, closeReq, closeCbs, opened, 
                                             lease >>
                        ELSE /\ cbs' = [cbs EXCEPT ![self] = closeCbs]
                             /\ closeCbs' = 0
                             /\ closeReq' = FALSE
                             /\ IF ptr
                                   THEN /\ sgot' = [sgot EXCEPT ![self] = lease]
                                        /\ lease' = FALSE
                                        /\ ptr' = FALSE
                                        /\ opened' = FALSE
                                   ELSE /\ TRUE
                                        /\ UNCHANGED << ptr, opened, lease, 
                                                        sgot >>
                             /\ Assert(mu = self, 
                                       "Failure of assertion at line 69, column 18 of macro called at line 285, column 9.")
                             /\ mu' = 0
                             /\ pc' = [pc EXCEPT ![self] = "s_job"]
                             /\ UNCHANGED << stack, isUser, chunks, cbop >>
                  /\ UNCHANGED << abandoned, ctxCalls, slotOnceDone, userRef, 
                                  weakNil, closerRef, finalized, queue, 
                                  wClosed, wExited, held, closing, driverFreed, 
                                  bg, handle, ffiBusy, slotReleases, 
                                  onDoneOwed, owed, withCb, got, cgot, cleanup, 
                                  fgot, i, op, ggot, lgot >>

s_job(self) == /\ pc[self] = "s_job"
               /\ IF sgot[self]
                     THEN /\ /\ cbop' = [cbop EXCEPT ![self] = Head(stack[self]).cbop]
                             /\ cbs' = [cbs EXCEPT ![self] = Head(stack[self]).cbs]
                             /\ chunks' = [chunks EXCEPT ![self] = Head(stack[self]).chunks]
                             /\ owed' = [owed EXCEPT ![self] = cbs[self]]
                             /\ sgot' = [sgot EXCEPT ![self] = Head(stack[self]).sgot]
                             /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseJob",
                                                                      pc        |->  Head(stack[self]).pc,
                                                                      owed      |->  owed[self] ] >>
                                                                  \o Tail(stack[self])]
                          /\ pc' = [pc EXCEPT ![self] = "cj_enq"]
                          /\ UNCHANGED << onDoneOwed, isUser >>
                     ELSE /\ onDoneOwed' = onDoneOwed - cbs[self]
                          /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                          /\ chunks' = [chunks EXCEPT ![self] = Head(stack[self]).chunks]
                          /\ sgot' = [sgot EXCEPT ![self] = Head(stack[self]).sgot]
                          /\ cbs' = [cbs EXCEPT ![self] = Head(stack[self]).cbs]
                          /\ cbop' = [cbop EXCEPT ![self] = Head(stack[self]).cbop]
                          /\ isUser' = [isUser EXCEPT ![self] = Head(stack[self]).isUser]
                          /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                          /\ owed' = owed
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, mu, lease, slotOnceDone, 
                               userRef, weakNil, closerRef, finalized, queue, 
                               wClosed, wExited, held, closing, driverFreed, 
                               bg, handle, ffiBusy, slotReleases, withCb, got, 
                               cgot, cleanup, fgot, i, op, ggot, lgot >>

StreamP(self) == s_check(self) \/ s_lock(self) \/ s_body(self)
                    \/ s_open(self) \/ s_next(self) \/ s_row(self)
                    \/ s_cb(self) \/ s_cb_run(self) \/ s_relock(self)
                    \/ s_drop(self) \/ s_finish(self) \/ s_job(self)

n_begin(self) == /\ pc[self] = "n_begin"
                 /\ IF abandoned \/ bg[Bg(0)] # "idle"
                       THEN /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                            /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                            /\ UNCHANGED << ctxCalls, bg >>
                       ELSE /\ ctxCalls' = ctxCalls + 1
                            /\ bg' = [bg EXCEPT ![Bg(0)] = "query"]
                            /\ pc' = [pc EXCEPT ![self] = "n_wait"]
                            /\ stack' = stack
                 /\ UNCHANGED << ptr, abandoned, streaming, closeReq, closeCbs, 
                                 opened, mu, lease, slotOnceDone, userRef, 
                                 weakNil, closerRef, finalized, queue, wClosed, 
                                 wExited, held, closing, driverFreed, handle, 
                                 ffiBusy, slotReleases, onDoneOwed, owed, 
                                 withCb, got, cgot, cleanup, fgot, isUser, 
                                 chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

n_wait(self) == /\ pc[self] = "n_wait"
                /\ \/ /\ bg[Bg(0)] = "done"
                      /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                      /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                      /\ UNCHANGED abandoned
                   \/ /\ IF ctxCalls > 0
                            THEN /\ abandoned' = TRUE
                            ELSE /\ TRUE
                                 /\ UNCHANGED abandoned
                      /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                      /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                /\ UNCHANGED << ptr, ctxCalls, streaming, closeReq, closeCbs, 
                                opened, mu, lease, slotOnceDone, userRef, 
                                weakNil, closerRef, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                owed, withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

NestedCtx(self) == n_begin(self) \/ n_wait(self)

u_loop(self) == /\ pc[self] = "u_loop"
                /\ IF i[self] < NOps
                      THEN /\ i' = [i EXCEPT ![self] = i[self] + 1]
                           /\ \E o \in Ops:
                                op' = [op EXCEPT ![self] = o]
                           /\ pc' = [pc EXCEPT ![self] = "u_dispatch"]
                      ELSE /\ pc' = [pc EXCEPT ![self] = "u_exit"]
                           /\ UNCHANGED << i, op >>
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, driverFreed, 
                                bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, ggot, lgot >>

u_dispatch(self) == /\ pc[self] = "u_dispatch"
                    /\ IF op[self] = "qctx" \/ op[self] = "qctxstream"
                          THEN /\ IF ~abandoned
                                     THEN /\ ctxCalls' = ctxCalls + 1
                                          /\ bg' = [bg EXCEPT ![Bg(i[self])] = IF op[self] = "qctx" THEN "query" ELSE "stream"]
                                          /\ pc' = [pc EXCEPT ![self] = "u_wait"]
                                     ELSE /\ pc' = [pc EXCEPT ![self] = "u_loop"]
                                          /\ UNCHANGED << ctxCalls, bg >>
                               /\ UNCHANGED << stack, withCb, got, cgot, 
                                               isUser, chunks, sgot, cbs, cbop >>
                          ELSE /\ IF op[self] = "stream"
                                     THEN /\ /\ isUser' = [isUser EXCEPT ![self] = TRUE]
                                             /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "StreamP",
                                                                                      pc        |->  "u_loop",
                                                                                      chunks    |->  chunks[self],
                                                                                      sgot      |->  sgot[self],
                                                                                      cbs       |->  cbs[self],
                                                                                      cbop      |->  cbop[self],
                                                                                      isUser    |->  isUser[self] ] >>
                                                                                  \o stack[self]]
                                          /\ chunks' = [chunks EXCEPT ![self] = 0]
                                          /\ sgot' = [sgot EXCEPT ![self] = FALSE]
                                          /\ cbs' = [cbs EXCEPT ![self] = 0]
                                          /\ cbop' = [cbop EXCEPT ![self] = "none"]
                                          /\ pc' = [pc EXCEPT ![self] = "s_check"]
                                          /\ UNCHANGED << withCb, got, cgot >>
                                     ELSE /\ IF op[self] = "commit"
                                                THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CommitP",
                                                                                              pc        |->  "u_loop" ] >>
                                                                                          \o stack[self]]
                                                     /\ pc' = [pc EXCEPT ![self] = "cm_check"]
                                                     /\ UNCHANGED << withCb, 
                                                                     got, cgot >>
                                                ELSE /\ IF op[self] = "rollback"
                                                           THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "RollbackP",
                                                                                                         pc        |->  "u_loop" ] >>
                                                                                                     \o stack[self]]
                                                                /\ pc' = [pc EXCEPT ![self] = "rb_check"]
                                                                /\ UNCHANGED << withCb, 
                                                                                got, 
                                                                                cgot >>
                                                           ELSE /\ IF op[self] = "close"
                                                                      THEN /\ /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseAsyncP",
                                                                                                                       pc        |->  "u_loop",
                                                                                                                       got       |->  got[self],
                                                                                                                       withCb    |->  withCb[self] ] >>
                                                                                                                   \o stack[self]]
                                                                              /\ withCb' = [withCb EXCEPT ![self] = TRUE]
                                                                           /\ got' = [got EXCEPT ![self] = FALSE]
                                                                           /\ pc' = [pc EXCEPT ![self] = "ca_abandon"]
                                                                           /\ cgot' = cgot
                                                                      ELSE /\ IF op[self] = "closechecked"
                                                                                 THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseCheckedP",
                                                                                                                               pc        |->  "u_loop",
                                                                                                                               cgot      |->  cgot[self] ] >>
                                                                                                                           \o stack[self]]
                                                                                      /\ cgot' = [cgot EXCEPT ![self] = FALSE]
                                                                                      /\ pc' = [pc EXCEPT ![self] = "cc_check"]
                                                                                 ELSE /\ IF op[self] = "isopen"
                                                                                            THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "IsOpenP",
                                                                                                                                          pc        |->  "u_loop" ] >>
                                                                                                                                      \o stack[self]]
                                                                                                 /\ pc' = [pc EXCEPT ![self] = "io_check"]
                                                                                            ELSE /\ pc' = [pc EXCEPT ![self] = "u_loop"]
                                                                                                 /\ stack' = stack
                                                                                      /\ cgot' = cgot
                                                                           /\ UNCHANGED << withCb, 
                                                                                           got >>
                                          /\ UNCHANGED << isUser, chunks, sgot, 
                                                          cbs, cbop >>
                               /\ UNCHANGED << ctxCalls, bg >>
                    /\ UNCHANGED << ptr, abandoned, streaming, closeReq, 
                                    closeCbs, opened, mu, lease, slotOnceDone, 
                                    userRef, weakNil, closerRef, finalized, 
                                    queue, wClosed, wExited, held, closing, 
                                    driverFreed, handle, ffiBusy, slotReleases, 
                                    onDoneOwed, owed, cleanup, fgot, i, op, 
                                    ggot, lgot >>

u_wait(self) == /\ pc[self] = "u_wait"
                /\ \/ /\ bg[Bg(i[self])] = "done"
                      /\ UNCHANGED abandoned
                   \/ /\ IF ctxCalls > 0
                            THEN /\ abandoned' = TRUE
                            ELSE /\ TRUE
                                 /\ UNCHANGED abandoned
                /\ pc' = [pc EXCEPT ![self] = "u_loop"]
                /\ UNCHANGED << ptr, ctxCalls, streaming, closeReq, closeCbs, 
                                opened, mu, lease, slotOnceDone, userRef, 
                                weakNil, closerRef, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

u_exit(self) == /\ pc[self] = "u_exit"
                /\ userRef' = FALSE
                /\ pc' = [pc EXCEPT ![self] = "Done"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                weakNil, closerRef, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

UserP(self) == u_loop(self) \/ u_dispatch(self) \/ u_wait(self)
                  \/ u_exit(self)

b_start(self) == /\ pc[self] = "b_start"
                 /\ bg[self] \in {"query", "stream"}
                 /\ IF bg[self] = "query"
                       THEN /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "QueryP",
                                                                     pc        |->  "b_finish" ] >>
                                                                 \o stack[self]]
                            /\ pc' = [pc EXCEPT ![self] = "q_check"]
                            /\ UNCHANGED << isUser, chunks, sgot, cbs, cbop >>
                       ELSE /\ /\ isUser' = [isUser EXCEPT ![self] = FALSE]
                               /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "StreamP",
                                                                        pc        |->  "b_finish",
                                                                        chunks    |->  chunks[self],
                                                                        sgot      |->  sgot[self],
                                                                        cbs       |->  cbs[self],
                                                                        cbop      |->  cbop[self],
                                                                        isUser    |->  isUser[self] ] >>
                                                                    \o stack[self]]
                            /\ chunks' = [chunks EXCEPT ![self] = 0]
                            /\ sgot' = [sgot EXCEPT ![self] = FALSE]
                            /\ cbs' = [cbs EXCEPT ![self] = 0]
                            /\ cbop' = [cbop EXCEPT ![self] = "none"]
                            /\ pc' = [pc EXCEPT ![self] = "s_check"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, mu, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 owed, withCb, got, cgot, cleanup, fgot, i, op, 
                                 ggot, lgot >>

b_finish(self) == /\ pc[self] = "b_finish"
                  /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "FinishCtx",
                                                           pc        |->  "b_done",
                                                           cleanup   |->  cleanup[self],
                                                           fgot      |->  fgot[self] ] >>
                                                       \o stack[self]]
                  /\ cleanup' = [cleanup EXCEPT ![self] = FALSE]
                  /\ fgot' = [fgot EXCEPT ![self] = FALSE]
                  /\ pc' = [pc EXCEPT ![self] = "fc_state"]
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wClosed, wExited, held, 
                                  closing, driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, owed, withCb, got, 
                                  cgot, isUser, chunks, sgot, cbs, cbop, i, op, 
                                  ggot, lgot >>

b_done(self) == /\ pc[self] = "b_done"
                /\ bg' = [bg EXCEPT ![self] = "done"]
                /\ pc' = [pc EXCEPT ![self] = "Done"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, driverFreed, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

BgP(self) == b_start(self) \/ b_finish(self) \/ b_done(self)

w_loop(self) == /\ pc[self] = "w_loop"
                /\ queue # <<>> \/ wClosed
                /\ IF queue = <<>>
                      THEN /\ wExited' = (wExited \cup {self})
                           /\ pc' = [pc EXCEPT ![self] = "w_end"]
                           /\ UNCHANGED << queue, held >>
                      ELSE /\ held' = [held EXCEPT ![self] = Head(queue)]
                           /\ queue' = Tail(queue)
                           /\ pc' = [pc EXCEPT ![self] = "w_run"]
                           /\ UNCHANGED wExited
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, 
                                wClosed, closing, driverFreed, bg, handle, 
                                ffiBusy, slotReleases, onDoneOwed, stack, owed, 
                                withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

w_run(self) == /\ pc[self] = "w_run"
               /\ Assert(handle = "live" /\ ~driverFreed, 
                         "Failure of assertion at line 71, column 18 of macro called at line 367, column 9.")
               /\ handle' = "freed"
               /\ IF ~slotOnceDone
                     THEN /\ slotOnceDone' = TRUE
                          /\ slotReleases' = slotReleases + 1
                     ELSE /\ TRUE
                          /\ UNCHANGED << slotOnceDone, slotReleases >>
               /\ onDoneOwed' = onDoneOwed - held[self]
               /\ held' = [held EXCEPT ![self] = -1]
               /\ pc' = [pc EXCEPT ![self] = "w_loop"]
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, mu, lease, userRef, weakNil, 
                               closerRef, finalized, queue, wClosed, wExited, 
                               closing, driverFreed, bg, ffiBusy, stack, owed, 
                               withCb, got, cgot, cleanup, fgot, isUser, 
                               chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

w_end(self) == /\ pc[self] = "w_end"
               /\ TRUE
               /\ pc' = [pc EXCEPT ![self] = "Done"]
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, mu, lease, slotOnceDone, 
                               userRef, weakNil, closerRef, finalized, queue, 
                               wClosed, wExited, held, closing, driverFreed, 
                               bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                               stack, owed, withCb, got, cgot, cleanup, fgot, 
                               isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                               lgot >>

WorkerP(self) == w_loop(self) \/ w_run(self) \/ w_end(self)

g_expire(self) == /\ pc[self] = "g_expire"
                  /\ ~Reachable
                  /\ weakNil' = TRUE
                  /\ pc' = [pc EXCEPT ![self] = "g_final"]
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, closerRef, finalized, 
                                  queue, wClosed, wExited, held, closing, 
                                  driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, stack, owed, 
                                  withCb, got, cgot, cleanup, fgot, isUser, 
                                  chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

g_final(self) == /\ pc[self] = "g_final"
                 /\ IF abandoned
                       THEN /\ pc' = [pc EXCEPT ![self] = "g_end"]
                       ELSE /\ pc' = [pc EXCEPT ![self] = "g_lock"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, mu, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, closing, driverFreed, 
                                 bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                                 stack, owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

g_lock(self) == /\ pc[self] = "g_lock"
                /\ mu = 0
                /\ mu' = self
                /\ pc' = [pc EXCEPT ![self] = "g_body"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, lease, slotOnceDone, userRef, 
                                weakNil, closerRef, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

g_body(self) == /\ pc[self] = "g_body"
                /\ IF streaming
                      THEN /\ Assert(mu = self, 
                                     "Failure of assertion at line 69, column 18 of macro called at line 389, column 22.")
                           /\ mu' = 0
                           /\ pc' = [pc EXCEPT ![self] = "g_end"]
                           /\ UNCHANGED << ptr, opened, lease, ggot >>
                      ELSE /\ IF ptr
                                 THEN /\ ggot' = [ggot EXCEPT ![self] = lease]
                                      /\ lease' = FALSE
                                      /\ ptr' = FALSE
                                      /\ opened' = FALSE
                                 ELSE /\ TRUE
                                      /\ UNCHANGED << ptr, opened, lease, ggot >>
                           /\ Assert(mu = self, 
                                     "Failure of assertion at line 69, column 18 of macro called at line 392, column 9.")
                           /\ mu' = 0
                           /\ pc' = [pc EXCEPT ![self] = "g_job"]
                /\ UNCHANGED << abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, slotOnceDone, userRef, weakNil, 
                                closerRef, finalized, queue, wClosed, wExited, 
                                held, closing, driverFreed, bg, handle, 
                                ffiBusy, slotReleases, onDoneOwed, stack, owed, 
                                withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, lgot >>

g_job(self) == /\ pc[self] = "g_job"
               /\ IF ggot[self]
                     THEN /\ /\ owed' = [owed EXCEPT ![self] = 0]
                             /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseJob",
                                                                      pc        |->  "g_end",
                                                                      owed      |->  owed[self] ] >>
                                                                  \o stack[self]]
                          /\ pc' = [pc EXCEPT ![self] = "cj_enq"]
                     ELSE /\ pc' = [pc EXCEPT ![self] = "g_end"]
                          /\ UNCHANGED << stack, owed >>
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, mu, lease, slotOnceDone, 
                               userRef, weakNil, closerRef, finalized, queue, 
                               wClosed, wExited, held, closing, driverFreed, 
                               bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                               withCb, got, cgot, cleanup, fgot, isUser, 
                               chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

g_end(self) == /\ pc[self] = "g_end"
               /\ finalized' = TRUE
               /\ pc' = [pc EXCEPT ![self] = "Done"]
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, mu, lease, slotOnceDone, 
                               userRef, weakNil, closerRef, queue, wClosed, 
                               wExited, held, closing, driverFreed, bg, handle, 
                               ffiBusy, slotReleases, onDoneOwed, stack, owed, 
                               withCb, got, cgot, cleanup, fgot, isUser, 
                               chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

GCP(self) == g_expire(self) \/ g_final(self) \/ g_lock(self)
                \/ g_body(self) \/ g_job(self) \/ g_end(self)

d_start(self) == /\ pc[self] = "d_start"
                 /\ IF ~WithDriverClose
                       THEN /\ pc' = [pc EXCEPT ![self] = "d_end"]
                            /\ UNCHANGED closing
                       ELSE /\ closing' = TRUE
                            /\ pc' = [pc EXCEPT ![self] = "d_open"]
                 /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                 closeCbs, opened, mu, lease, slotOnceDone, 
                                 userRef, weakNil, closerRef, finalized, queue, 
                                 wClosed, wExited, held, driverFreed, bg, 
                                 handle, ffiBusy, slotReleases, onDoneOwed, 
                                 stack, owed, withCb, got, cgot, cleanup, fgot, 
                                 isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                 lgot >>

d_open(self) == /\ pc[self] = "d_open"
                /\ IF ~weakNil /\ opened
                      THEN /\ closerRef' = TRUE
                           /\ pc' = [pc EXCEPT ![self] = "d_close_tx"]
                      ELSE /\ pc' = [pc EXCEPT ![self] = "d_orphans"]
                           /\ UNCHANGED closerRef
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, finalized, queue, wClosed, 
                                wExited, held, closing, driverFreed, bg, 
                                handle, ffiBusy, slotReleases, onDoneOwed, 
                                stack, owed, withCb, got, cgot, cleanup, fgot, 
                                isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                                lgot >>

d_close_tx(self) == /\ pc[self] = "d_close_tx"
                    /\ /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "CloseAsyncP",
                                                                pc        |->  "d_drop_ref",
                                                                got       |->  got[self],
                                                                withCb    |->  withCb[self] ] >>
                                                            \o stack[self]]
                       /\ withCb' = [withCb EXCEPT ![self] = FALSE]
                    /\ got' = [got EXCEPT ![self] = FALSE]
                    /\ pc' = [pc EXCEPT ![self] = "ca_abandon"]
                    /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                    closeReq, closeCbs, opened, mu, lease, 
                                    slotOnceDone, userRef, weakNil, closerRef, 
                                    finalized, queue, wClosed, wExited, held, 
                                    closing, driverFreed, bg, handle, ffiBusy, 
                                    slotReleases, onDoneOwed, owed, cgot, 
                                    cleanup, fgot, isUser, chunks, sgot, cbs, 
                                    cbop, i, op, ggot, lgot >>

d_drop_ref(self) == /\ pc[self] = "d_drop_ref"
                    /\ closerRef' = FALSE
                    /\ pc' = [pc EXCEPT ![self] = "d_orphans"]
                    /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                    closeReq, closeCbs, opened, mu, lease, 
                                    slotOnceDone, userRef, weakNil, finalized, 
                                    queue, wClosed, wExited, held, closing, 
                                    driverFreed, bg, handle, ffiBusy, 
                                    slotReleases, onDoneOwed, stack, owed, 
                                    withCb, got, cgot, cleanup, fgot, isUser, 
                                    chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

d_orphans(self) == /\ pc[self] = "d_orphans"
                   /\ IF weakNil /\ lease
                         THEN /\ lease' = FALSE
                              /\ lgot' = [lgot EXCEPT ![self] = TRUE]
                         ELSE /\ TRUE
                              /\ UNCHANGED << lease, lgot >>
                   /\ pc' = [pc EXCEPT ![self] = "d_run_orphan"]
                   /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                   closeReq, closeCbs, opened, mu, 
                                   slotOnceDone, userRef, weakNil, closerRef, 
                                   finalized, queue, wClosed, wExited, held, 
                                   closing, driverFreed, bg, handle, ffiBusy, 
                                   slotReleases, onDoneOwed, stack, owed, 
                                   withCb, got, cgot, cleanup, fgot, isUser, 
                                   chunks, sgot, cbs, cbop, i, op, ggot >>

d_run_orphan(self) == /\ pc[self] = "d_run_orphan"
                      /\ IF lgot[self]
                            THEN /\ Assert(handle = "live" /\ ~driverFreed, 
                                           "Failure of assertion at line 71, column 18 of macro called at line 416, column 17.")
                                 /\ handle' = "freed"
                                 /\ slotReleases' = slotReleases + 1
                            ELSE /\ TRUE
                                 /\ UNCHANGED << handle, slotReleases >>
                      /\ pc' = [pc EXCEPT ![self] = "d_worker"]
                      /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                      closeReq, closeCbs, opened, mu, lease, 
                                      slotOnceDone, userRef, weakNil, 
                                      closerRef, finalized, queue, wClosed, 
                                      wExited, held, closing, driverFreed, bg, 
                                      ffiBusy, onDoneOwed, stack, owed, withCb, 
                                      got, cgot, cleanup, fgot, isUser, chunks, 
                                      sgot, cbs, cbop, i, op, ggot, lgot >>

d_worker(self) == /\ pc[self] = "d_worker"
                  /\ wClosed' = TRUE
                  /\ pc' = [pc EXCEPT ![self] = "d_worker_wait"]
                  /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                  closeReq, closeCbs, opened, mu, lease, 
                                  slotOnceDone, userRef, weakNil, closerRef, 
                                  finalized, queue, wExited, held, closing, 
                                  driverFreed, bg, handle, ffiBusy, 
                                  slotReleases, onDoneOwed, stack, owed, 
                                  withCb, got, cgot, cleanup, fgot, isUser, 
                                  chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

d_worker_wait(self) == /\ pc[self] = "d_worker_wait"
                       /\ WDone
                       /\ pc' = [pc EXCEPT ![self] = "d_wg"]
                       /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, 
                                       closeReq, closeCbs, opened, mu, lease, 
                                       slotOnceDone, userRef, weakNil, 
                                       closerRef, finalized, queue, wClosed, 
                                       wExited, held, closing, driverFreed, bg, 
                                       handle, ffiBusy, slotReleases, 
                                       onDoneOwed, stack, owed, withCb, got, 
                                       cgot, cleanup, fgot, isUser, chunks, 
                                       sgot, cbs, cbop, i, op, ggot, lgot >>

d_wg(self) == /\ pc[self] = "d_wg"
              /\ slotReleases >= 1
              /\ pc' = [pc EXCEPT ![self] = "d_free"]
              /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                              closeCbs, opened, mu, lease, slotOnceDone, 
                              userRef, weakNil, closerRef, finalized, queue, 
                              wClosed, wExited, held, closing, driverFreed, bg, 
                              handle, ffiBusy, slotReleases, onDoneOwed, stack, 
                              owed, withCb, got, cgot, cleanup, fgot, isUser, 
                              chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

d_free(self) == /\ pc[self] = "d_free"
                /\ driverFreed' = TRUE
                /\ pc' = [pc EXCEPT ![self] = "d_end"]
                /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                                closeCbs, opened, mu, lease, slotOnceDone, 
                                userRef, weakNil, closerRef, finalized, queue, 
                                wClosed, wExited, held, closing, bg, handle, 
                                ffiBusy, slotReleases, onDoneOwed, stack, owed, 
                                withCb, got, cgot, cleanup, fgot, isUser, 
                                chunks, sgot, cbs, cbop, i, op, ggot, lgot >>

d_end(self) == /\ pc[self] = "d_end"
               /\ TRUE
               /\ pc' = [pc EXCEPT ![self] = "Done"]
               /\ UNCHANGED << ptr, abandoned, ctxCalls, streaming, closeReq, 
                               closeCbs, opened, mu, lease, slotOnceDone, 
                               userRef, weakNil, closerRef, finalized, queue, 
                               wClosed, wExited, held, closing, driverFreed, 
                               bg, handle, ffiBusy, slotReleases, onDoneOwed, 
                               stack, owed, withCb, got, cgot, cleanup, fgot, 
                               isUser, chunks, sgot, cbs, cbop, i, op, ggot, 
                               lgot >>

CloserP(self) == d_start(self) \/ d_open(self) \/ d_close_tx(self)
                    \/ d_drop_ref(self) \/ d_orphans(self)
                    \/ d_run_orphan(self) \/ d_worker(self)
                    \/ d_worker_wait(self) \/ d_wg(self) \/ d_free(self)
                    \/ d_end(self)

(* Allow infinite stuttering to prevent deadlock on termination. *)
Terminating == /\ \A self \in ProcSet: pc[self] = "Done"
               /\ UNCHANGED vars

Next == (\E self \in ProcSet:  \/ CloseJob(self) \/ CloseAsyncP(self)
                               \/ CommitP(self) \/ RollbackP(self)
                               \/ CloseCheckedP(self) \/ IsOpenP(self)
                               \/ FinishCtx(self) \/ QueryP(self)
                               \/ StreamP(self) \/ NestedCtx(self))
           \/ (\E self \in {User}: UserP(self))
           \/ (\E self \in BgIds: BgP(self))
           \/ (\E self \in Workers: WorkerP(self))
           \/ (\E self \in {GC}: GCP(self))
           \/ (\E self \in {Closer}: CloserP(self))
           \/ Terminating

Spec == /\ Init /\ [][Next]_vars
        /\ \A self \in {User} : /\ WF_vars(UserP(self))
                                /\ WF_vars(StreamP(self))
                                /\ WF_vars(CommitP(self))
                                /\ WF_vars(RollbackP(self))
                                /\ WF_vars(CloseAsyncP(self))
                                /\ WF_vars(CloseCheckedP(self))
                                /\ WF_vars(IsOpenP(self))
                                /\ WF_vars(CloseJob(self))
                                /\ WF_vars(NestedCtx(self))
        /\ \A self \in BgIds : /\ WF_vars(BgP(self))
                               /\ WF_vars(QueryP(self))
                               /\ WF_vars(StreamP(self))
                               /\ WF_vars(FinishCtx(self))
                               /\ WF_vars(CloseJob(self))
                               /\ WF_vars(CloseAsyncP(self))
                               /\ WF_vars(CommitP(self))
                               /\ WF_vars(IsOpenP(self))
                               /\ WF_vars(NestedCtx(self))
        /\ \A self \in Workers : WF_vars(WorkerP(self))
        /\ \A self \in {GC} : WF_vars(GCP(self)) /\ WF_vars(CloseJob(self))
        /\ \A self \in {Closer} : /\ WF_vars(CloserP(self))
                                  /\ WF_vars(CloseAsyncP(self))
                                  /\ WF_vars(CloseJob(self))

Termination == <>(\A self \in ProcSet: pc[self] = "Done")

\* END TRANSLATION

EventuallyClean == <>[]Clean
CloserFinishes  == <>(pc[Closer] = "Done")
=============================================================================
