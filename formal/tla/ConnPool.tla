---------------------------- MODULE ConnPool ----------------------------
(***************************************************************************)
(* Model of gotype/pool.go (ConnPool).                                     *)
(*                                                                         *)
(* Each labelled step is one critical section of p.mu, or one step taken   *)
(* outside the lock (IsOpen, the factory call, a channel receive).          *)
(* Ghost variable `where` records who owns each connection so that the     *)
(* numOpen accounting can be checked against reality.                       *)
(***************************************************************************)
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS Clients,      \* goroutines (small integers) that call Get / Put
          MaxSize,      \* PoolConfig.MaxSize (> 0 in this model)
          MinSize,      \* PoolConfig.MinSize
          MaxIds,       \* bound on connections the factory can create
          Iters,        \* Get/Put rounds per client
          Timeouts,     \* TRUE: waiters and dials may time out / be cancelled
          MaxKills      \* bound on connections that die while in use or idle

NoMsg  == -1
ErrMsg == -2
Retry  == 0
NoConn == -1
Ids    == 1..MaxIds

(* --algorithm ConnPool {
variables
    conns     = [i \in 1..MinSize |-> i],   \* idle stack (Go slice, LIFO)
    numOpen   = MinSize,
    waitQ     = <<>>,
    closed    = FALSE,
    chan      = [c \in Clients |-> NoMsg],  \* poolWaiter.result, capacity 1
    nextId    = MinSize + 1,
    alive     = [i \in Ids |-> TRUE],
    kills     = 0,
    \* ghost: owner of each connection
    where     = [i \in Ids |-> IF i <= MinSize THEN "idle" ELSE "none"],
    dialing   = 0,                          \* reserved slots with no conn yet
    orphan    = [c \in Clients |-> FALSE];  \* dial handed to a background goroutine

define {
    Tracked == {i \in Ids : where[i] \in {"idle", "held", "chan", "putting"}}
    InConns(i) == \E k \in 1..Len(conns) : conns[k] = i

    TypeOK == /\ numOpen \in Int
              /\ \A k \in 1..Len(conns) : conns[k] \in Ids

    \* numOpen counts idle + checked-out + in-flight dial reservations.
    Accounting == numOpen = Cardinality(Tracked) + dialing

    CapacityBound == numOpen <= MaxSize /\ numOpen >= 0

    \* A connection is in the idle stack iff the ghost says idle, at most once.
    IdleConsistent ==
        /\ \A i \in Ids : (where[i] = "idle") <=> InConns(i)
        /\ \A j, k \in 1..Len(conns) : j # k => conns[j] # conns[k]

    \* The pool never keeps idle connections while waiters are queued.
    NoIdleWithWaiters == (waitQ # <<>>) => (conns = <<>>)

    \* After Close, the pool holds no idle connections.
    ClosedEmpty == closed => conns = <<>>

    \* When everything has finished, no connection has leaked.
    AllDone == \A c \in Clients : pc[c] = "Done"
    NoLeakAtEnd == (AllDone /\ closed /\ \A c \in Clients : ~orphan[c])
                      => (numOpen = 0 /\ Tracked = {})
}

macro wake() {
    if (waitQ # <<>>) {
        assert chan[Head(waitQ)] = NoMsg;   \* else the send would block under p.mu
        chan[Head(waitQ)] := Retry;
        waitQ := Tail(waitQ);
    }
}

procedure Put(pconn = NoConn)
variables h = FALSE;
{
p_health:                       \* conn.IsOpen() outside the lock
    h := alive[pconn];
    where[pconn] := "putting";
p_lock:
    if (closed) {
        numOpen := numOpen - 1;
        where[pconn] := "discarded";
    } else if (~h) {
        numOpen := numOpen - 1;
        wake();
        where[pconn] := "discarded";
    } else if (waitQ # <<>>) {
        assert chan[Head(waitQ)] = NoMsg;
        chan[Head(waitQ)] := pconn;
        where[pconn] := "chan";
        waitQ := Tail(waitQ);
    } else {
        conns := Append(conns, pconn);
        where[pconn] := "idle";
    };
    return;
}

fair process (Client \in Clients)
variables held = NoConn, iter = 0, msg = NoMsg, healthy = TRUE;
{
c_top:
    while (iter < Iters) {
        iter := iter + 1;
g_lock:
        if (closed) {
            goto c_top;                          \* ErrPoolClosed
        } else if (Len(conns) > 0) {
            held := conns[Len(conns)];
            conns := SubSeq(conns, 1, Len(conns) - 1);
            where[held] := "held";
            goto g_validate;
        } else if (numOpen < MaxSize) {
            numOpen := numOpen + 1;
            dialing := dialing + 1;
            goto g_dial;
        } else {
            waitQ := Append(waitQ, self);
            goto g_await;
        };
g_validate:                                      \* IsOpen outside the lock
        healthy := alive[held];
g_validate_lock:
        if (healthy) {
            if (closed) {
                numOpen := numOpen - 1;
                where[held] := "discarded";
                held := NoConn;
                goto c_top;
            } else {
                goto c_use;
            }
        } else {
            numOpen := numOpen - 1;
            wake();
            where[held] := "discarded";
            held := NoConn;
            goto g_lock;
        };
g_dial:
        either {                                 \* factory succeeds
            await nextId <= MaxIds;
            held := nextId;
            nextId := nextId + 1;
            where[held] := "held";
            dialing := dialing - 1;
            goto g_dial_lock;
        } or {                                   \* factory fails
            numOpen := numOpen - 1;
            dialing := dialing - 1;
            wake();
            goto c_top;
        } or {                                   \* ctx cancelled: background owns slot
            await Timeouts /\ ~orphan[self];
            orphan[self] := TRUE;
            goto c_top;
        };
g_dial_lock:
        if (closed) {
            numOpen := numOpen - 1;
            where[held] := "discarded";
            held := NoConn;
            goto c_top;
        } else {
            goto c_use;
        };
g_await:
        either {
            await chan[self] # NoMsg;
            msg := chan[self];
            chan[self] := NoMsg;
            if (msg = ErrMsg) {
                goto c_top;
            } else if (msg = Retry) {
                goto g_lock;
            } else {
                held := msg;
                where[held] := "held";
                goto g_handoff_lock;
            }
        } or {
            await Timeouts;
            goto t_remove;
        };
g_handoff_lock:
        if (closed) {
            numOpen := numOpen - 1;
            where[held] := "discarded";
            held := NoConn;
            goto c_top;
        } else {
            goto c_use;
        };
t_remove:                                        \* removeWaiter
        waitQ := SelectSeq(waitQ, LAMBDA x : x # self);
t_drain:                                         \* non-blocking drain
        msg := chan[self];
        chan[self] := NoMsg;
        if (msg > 0) {
            where[msg] := "held";
            call Put(msg);
            goto c_top;
        } else if (msg = Retry) {
            goto t_forward;
        } else {
            goto c_top;
        };
t_forward:
        wake();
        goto c_top;
c_use:
        skip;
c_put:
        call Put(held);
c_after:
        held := NoConn;
    }
}

\* Background goroutine that owns a cancelled dial.
fair process (Orphan \in {100 + c : c \in Clients})
variables oconn = NoConn;
{
o_wait:
    while (TRUE) {
        await orphan[(self - 100)];
        either {
            await nextId <= MaxIds;
            oconn := nextId;
            nextId := nextId + 1;
            where[oconn] := "held";
            dialing := dialing - 1;
o_put:
            call Put(oconn);
o_clear:
            orphan[(self - 100)] := FALSE;
        } or {
            numOpen := numOpen - 1;
            dialing := dialing - 1;
            wake();
            orphan[(self - 100)] := FALSE;
        }
    }
}

fair process (Cleaner = 200)
{
cl_loop:
    while (TRUE) {
        \* Reap one expired idle connection while above MinSize. The ticker may
        \* still fire after Close (select is random), so no ~closed guard.
        await Len(conns) > MinSize;
        with (k \in 1..Len(conns)) {
            where[conns[k]] := "discarded";
            conns := [j \in 1..(Len(conns) - 1) |-> IF j < k THEN conns[j] ELSE conns[j + 1]];
        };
        numOpen := numOpen - 1;
        wake();
    }
}

fair process (Closer = 201)
{
cs_start:
    either { skip } or {
        closed := TRUE;
        where := [i \in Ids |-> IF where[i] = "idle" THEN "discarded" ELSE where[i]];
        numOpen := numOpen - Len(conns);
        conns := <<>>;
        assert \A k \in 1..Len(waitQ) : chan[waitQ[k]] = NoMsg;
        chan := [c \in Clients |-> IF \E k \in 1..Len(waitQ) : waitQ[k] = c
                                   THEN ErrMsg ELSE chan[c]];
        waitQ := <<>>;
    }
}

\* A connection dies (server gone, network drop).
process (Env = 202)
{
e_loop:
    while (TRUE) {
        await kills < MaxKills;
        with (i \in {j \in Ids : where[j] \in {"idle", "held"} /\ alive[j]}) {
            alive[i] := FALSE;
        };
        kills := kills + 1;
    }
}
} *)
\* BEGIN TRANSLATION
VARIABLES conns, numOpen, waitQ, closed, chan, nextId, alive, kills, where, 
          dialing, orphan, pc, stack

(* define statement *)
Tracked == {i \in Ids : where[i] \in {"idle", "held", "chan", "putting"}}
InConns(i) == \E k \in 1..Len(conns) : conns[k] = i

TypeOK == /\ numOpen \in Int
          /\ \A k \in 1..Len(conns) : conns[k] \in Ids


Accounting == numOpen = Cardinality(Tracked) + dialing

CapacityBound == numOpen <= MaxSize /\ numOpen >= 0


IdleConsistent ==
    /\ \A i \in Ids : (where[i] = "idle") <=> InConns(i)
    /\ \A j, k \in 1..Len(conns) : j # k => conns[j] # conns[k]


NoIdleWithWaiters == (waitQ # <<>>) => (conns = <<>>)


ClosedEmpty == closed => conns = <<>>


AllDone == \A c \in Clients : pc[c] = "Done"
NoLeakAtEnd == (AllDone /\ closed /\ \A c \in Clients : ~orphan[c])
                  => (numOpen = 0 /\ Tracked = {})

VARIABLES pconn, h, held, iter, msg, healthy, oconn

vars == << conns, numOpen, waitQ, closed, chan, nextId, alive, kills, where, 
           dialing, orphan, pc, stack, pconn, h, held, iter, msg, healthy, 
           oconn >>

ProcSet == (Clients) \cup ({100 + c : c \in Clients}) \cup {200} \cup {201} \cup {202}

Init == (* Global variables *)
        /\ conns = [i \in 1..MinSize |-> i]
        /\ numOpen = MinSize
        /\ waitQ = <<>>
        /\ closed = FALSE
        /\ chan = [c \in Clients |-> NoMsg]
        /\ nextId = MinSize + 1
        /\ alive = [i \in Ids |-> TRUE]
        /\ kills = 0
        /\ where = [i \in Ids |-> IF i <= MinSize THEN "idle" ELSE "none"]
        /\ dialing = 0
        /\ orphan = [c \in Clients |-> FALSE]
        (* Procedure Put *)
        /\ pconn = [ self \in ProcSet |-> NoConn]
        /\ h = [ self \in ProcSet |-> FALSE]
        (* Process Client *)
        /\ held = [self \in Clients |-> NoConn]
        /\ iter = [self \in Clients |-> 0]
        /\ msg = [self \in Clients |-> NoMsg]
        /\ healthy = [self \in Clients |-> TRUE]
        (* Process Orphan *)
        /\ oconn = [self \in {100 + c : c \in Clients} |-> NoConn]
        /\ stack = [self \in ProcSet |-> << >>]
        /\ pc = [self \in ProcSet |-> CASE self \in Clients -> "c_top"
                                        [] self \in {100 + c : c \in Clients} -> "o_wait"
                                        [] self = 200 -> "cl_loop"
                                        [] self = 201 -> "cs_start"
                                        [] self = 202 -> "e_loop"]

p_health(self) == /\ pc[self] = "p_health"
                  /\ h' = [h EXCEPT ![self] = alive[pconn[self]]]
                  /\ where' = [where EXCEPT ![pconn[self]] = "putting"]
                  /\ pc' = [pc EXCEPT ![self] = "p_lock"]
                  /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, 
                                  alive, kills, dialing, orphan, stack, pconn, 
                                  held, iter, msg, healthy, oconn >>

p_lock(self) == /\ pc[self] = "p_lock"
                /\ IF closed
                      THEN /\ numOpen' = numOpen - 1
                           /\ where' = [where EXCEPT ![pconn[self]] = "discarded"]
                           /\ UNCHANGED << conns, waitQ, chan >>
                      ELSE /\ IF ~h[self]
                                 THEN /\ numOpen' = numOpen - 1
                                      /\ IF waitQ # <<>>
                                            THEN /\ Assert(chan[Head(waitQ)] = NoMsg, 
                                                           "Failure of assertion at line 72, column 9 of macro called at line 90, column 9.")
                                                 /\ chan' = [chan EXCEPT ![Head(waitQ)] = Retry]
                                                 /\ waitQ' = Tail(waitQ)
                                            ELSE /\ TRUE
                                                 /\ UNCHANGED << waitQ, chan >>
                                      /\ where' = [where EXCEPT ![pconn[self]] = "discarded"]
                                      /\ conns' = conns
                                 ELSE /\ IF waitQ # <<>>
                                            THEN /\ Assert(chan[Head(waitQ)] = NoMsg, 
                                                           "Failure of assertion at line 93, column 9.")
                                                 /\ chan' = [chan EXCEPT ![Head(waitQ)] = pconn[self]]
                                                 /\ where' = [where EXCEPT ![pconn[self]] = "chan"]
                                                 /\ waitQ' = Tail(waitQ)
                                                 /\ conns' = conns
                                            ELSE /\ conns' = Append(conns, pconn[self])
                                                 /\ where' = [where EXCEPT ![pconn[self]] = "idle"]
                                                 /\ UNCHANGED << waitQ, chan >>
                                      /\ UNCHANGED numOpen
                /\ pc' = [pc EXCEPT ![self] = Head(stack[self]).pc]
                /\ h' = [h EXCEPT ![self] = Head(stack[self]).h]
                /\ pconn' = [pconn EXCEPT ![self] = Head(stack[self]).pconn]
                /\ stack' = [stack EXCEPT ![self] = Tail(stack[self])]
                /\ UNCHANGED << closed, nextId, alive, kills, dialing, orphan, 
                                held, iter, msg, healthy, oconn >>

Put(self) == p_health(self) \/ p_lock(self)

c_top(self) == /\ pc[self] = "c_top"
               /\ IF iter[self] < Iters
                     THEN /\ iter' = [iter EXCEPT ![self] = iter[self] + 1]
                          /\ pc' = [pc EXCEPT ![self] = "g_lock"]
                     ELSE /\ pc' = [pc EXCEPT ![self] = "Done"]
                          /\ iter' = iter
               /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, 
                               alive, kills, where, dialing, orphan, stack, 
                               pconn, h, held, msg, healthy, oconn >>

g_lock(self) == /\ pc[self] = "g_lock"
                /\ IF closed
                      THEN /\ pc' = [pc EXCEPT ![self] = "c_top"]
                           /\ UNCHANGED << conns, numOpen, waitQ, where, 
                                           dialing, held >>
                      ELSE /\ IF Len(conns) > 0
                                 THEN /\ held' = [held EXCEPT ![self] = conns[Len(conns)]]
                                      /\ conns' = SubSeq(conns, 1, Len(conns) - 1)
                                      /\ where' = [where EXCEPT ![held'[self]] = "held"]
                                      /\ pc' = [pc EXCEPT ![self] = "g_validate"]
                                      /\ UNCHANGED << numOpen, waitQ, dialing >>
                                 ELSE /\ IF numOpen < MaxSize
                                            THEN /\ numOpen' = numOpen + 1
                                                 /\ dialing' = dialing + 1
                                                 /\ pc' = [pc EXCEPT ![self] = "g_dial"]
                                                 /\ waitQ' = waitQ
                                            ELSE /\ waitQ' = Append(waitQ, self)
                                                 /\ pc' = [pc EXCEPT ![self] = "g_await"]
                                                 /\ UNCHANGED << numOpen, 
                                                                 dialing >>
                                      /\ UNCHANGED << conns, where, held >>
                /\ UNCHANGED << closed, chan, nextId, alive, kills, orphan, 
                                stack, pconn, h, iter, msg, healthy, oconn >>

g_validate(self) == /\ pc[self] = "g_validate"
                    /\ healthy' = [healthy EXCEPT ![self] = alive[held[self]]]
                    /\ pc' = [pc EXCEPT ![self] = "g_validate_lock"]
                    /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, 
                                    nextId, alive, kills, where, dialing, 
                                    orphan, stack, pconn, h, held, iter, msg, 
                                    oconn >>

g_validate_lock(self) == /\ pc[self] = "g_validate_lock"
                         /\ IF healthy[self]
                               THEN /\ IF closed
                                          THEN /\ numOpen' = numOpen - 1
                                               /\ where' = [where EXCEPT ![held[self]] = "discarded"]
                                               /\ held' = [held EXCEPT ![self] = NoConn]
                                               /\ pc' = [pc EXCEPT ![self] = "c_top"]
                                          ELSE /\ pc' = [pc EXCEPT ![self] = "c_use"]
                                               /\ UNCHANGED << numOpen, where, 
                                                               held >>
                                    /\ UNCHANGED << waitQ, chan >>
                               ELSE /\ numOpen' = numOpen - 1
                                    /\ IF waitQ # <<>>
                                          THEN /\ Assert(chan[Head(waitQ)] = NoMsg, 
                                                         "Failure of assertion at line 72, column 9 of macro called at line 140, column 13.")
                                               /\ chan' = [chan EXCEPT ![Head(waitQ)] = Retry]
                                               /\ waitQ' = Tail(waitQ)
                                          ELSE /\ TRUE
                                               /\ UNCHANGED << waitQ, chan >>
                                    /\ where' = [where EXCEPT ![held[self]] = "discarded"]
                                    /\ held' = [held EXCEPT ![self] = NoConn]
                                    /\ pc' = [pc EXCEPT ![self] = "g_lock"]
                         /\ UNCHANGED << conns, closed, nextId, alive, kills, 
                                         dialing, orphan, stack, pconn, h, 
                                         iter, msg, healthy, oconn >>

g_dial(self) == /\ pc[self] = "g_dial"
                /\ \/ /\ nextId <= MaxIds
                      /\ held' = [held EXCEPT ![self] = nextId]
                      /\ nextId' = nextId + 1
                      /\ where' = [where EXCEPT ![held'[self]] = "held"]
                      /\ dialing' = dialing - 1
                      /\ pc' = [pc EXCEPT ![self] = "g_dial_lock"]
                      /\ UNCHANGED <<numOpen, waitQ, chan, orphan>>
                   \/ /\ numOpen' = numOpen - 1
                      /\ dialing' = dialing - 1
                      /\ IF waitQ # <<>>
                            THEN /\ Assert(chan[Head(waitQ)] = NoMsg, 
                                           "Failure of assertion at line 72, column 9 of macro called at line 156, column 13.")
                                 /\ chan' = [chan EXCEPT ![Head(waitQ)] = Retry]
                                 /\ waitQ' = Tail(waitQ)
                            ELSE /\ TRUE
                                 /\ UNCHANGED << waitQ, chan >>
                      /\ pc' = [pc EXCEPT ![self] = "c_top"]
                      /\ UNCHANGED <<nextId, where, orphan, held>>
                   \/ /\ Timeouts /\ ~orphan[self]
                      /\ orphan' = [orphan EXCEPT ![self] = TRUE]
                      /\ pc' = [pc EXCEPT ![self] = "c_top"]
                      /\ UNCHANGED <<numOpen, waitQ, chan, nextId, where, dialing, held>>
                /\ UNCHANGED << conns, closed, alive, kills, stack, pconn, h, 
                                iter, msg, healthy, oconn >>

g_dial_lock(self) == /\ pc[self] = "g_dial_lock"
                     /\ IF closed
                           THEN /\ numOpen' = numOpen - 1
                                /\ where' = [where EXCEPT ![held[self]] = "discarded"]
                                /\ held' = [held EXCEPT ![self] = NoConn]
                                /\ pc' = [pc EXCEPT ![self] = "c_top"]
                           ELSE /\ pc' = [pc EXCEPT ![self] = "c_use"]
                                /\ UNCHANGED << numOpen, where, held >>
                     /\ UNCHANGED << conns, waitQ, closed, chan, nextId, alive, 
                                     kills, dialing, orphan, stack, pconn, h, 
                                     iter, msg, healthy, oconn >>

g_await(self) == /\ pc[self] = "g_await"
                 /\ \/ /\ chan[self] # NoMsg
                       /\ msg' = [msg EXCEPT ![self] = chan[self]]
                       /\ chan' = [chan EXCEPT ![self] = NoMsg]
                       /\ IF msg'[self] = ErrMsg
                             THEN /\ pc' = [pc EXCEPT ![self] = "c_top"]
                                  /\ UNCHANGED << where, held >>
                             ELSE /\ IF msg'[self] = Retry
                                        THEN /\ pc' = [pc EXCEPT ![self] = "g_lock"]
                                             /\ UNCHANGED << where, held >>
                                        ELSE /\ held' = [held EXCEPT ![self] = msg'[self]]
                                             /\ where' = [where EXCEPT ![held'[self]] = "held"]
                                             /\ pc' = [pc EXCEPT ![self] = "g_handoff_lock"]
                    \/ /\ Timeouts
                       /\ pc' = [pc EXCEPT ![self] = "t_remove"]
                       /\ UNCHANGED <<chan, where, held, msg>>
                 /\ UNCHANGED << conns, numOpen, waitQ, closed, nextId, alive, 
                                 kills, dialing, orphan, stack, pconn, h, iter, 
                                 healthy, oconn >>

g_handoff_lock(self) == /\ pc[self] = "g_handoff_lock"
                        /\ IF closed
                              THEN /\ numOpen' = numOpen - 1
                                   /\ where' = [where EXCEPT ![held[self]] = "discarded"]
                                   /\ held' = [held EXCEPT ![self] = NoConn]
                                   /\ pc' = [pc EXCEPT ![self] = "c_top"]
                              ELSE /\ pc' = [pc EXCEPT ![self] = "c_use"]
                                   /\ UNCHANGED << numOpen, where, held >>
                        /\ UNCHANGED << conns, waitQ, closed, chan, nextId, 
                                        alive, kills, dialing, orphan, stack, 
                                        pconn, h, iter, msg, healthy, oconn >>

t_remove(self) == /\ pc[self] = "t_remove"
                  /\ waitQ' = SelectSeq(waitQ, LAMBDA x : x # self)
                  /\ pc' = [pc EXCEPT ![self] = "t_drain"]
                  /\ UNCHANGED << conns, numOpen, closed, chan, nextId, alive, 
                                  kills, where, dialing, orphan, stack, pconn, 
                                  h, held, iter, msg, healthy, oconn >>

t_drain(self) == /\ pc[self] = "t_drain"
                 /\ msg' = [msg EXCEPT ![self] = chan[self]]
                 /\ chan' = [chan EXCEPT ![self] = NoMsg]
                 /\ IF msg'[self] > 0
                       THEN /\ where' = [where EXCEPT ![msg'[self]] = "held"]
                            /\ /\ pconn' = [pconn EXCEPT ![self] = msg'[self]]
                               /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "Put",
                                                                        pc        |->  "c_top",
                                                                        h         |->  h[self],
                                                                        pconn     |->  pconn[self] ] >>
                                                                    \o stack[self]]
                            /\ h' = [h EXCEPT ![self] = FALSE]
                            /\ pc' = [pc EXCEPT ![self] = "p_health"]
                       ELSE /\ IF msg'[self] = Retry
                                  THEN /\ pc' = [pc EXCEPT ![self] = "t_forward"]
                                  ELSE /\ pc' = [pc EXCEPT ![self] = "c_top"]
                            /\ UNCHANGED << where, stack, pconn, h >>
                 /\ UNCHANGED << conns, numOpen, waitQ, closed, nextId, alive, 
                                 kills, dialing, orphan, held, iter, healthy, 
                                 oconn >>

t_forward(self) == /\ pc[self] = "t_forward"
                   /\ IF waitQ # <<>>
                         THEN /\ Assert(chan[Head(waitQ)] = NoMsg, 
                                        "Failure of assertion at line 72, column 9 of macro called at line 214, column 9.")
                              /\ chan' = [chan EXCEPT ![Head(waitQ)] = Retry]
                              /\ waitQ' = Tail(waitQ)
                         ELSE /\ TRUE
                              /\ UNCHANGED << waitQ, chan >>
                   /\ pc' = [pc EXCEPT ![self] = "c_top"]
                   /\ UNCHANGED << conns, numOpen, closed, nextId, alive, 
                                   kills, where, dialing, orphan, stack, pconn, 
                                   h, held, iter, msg, healthy, oconn >>

c_use(self) == /\ pc[self] = "c_use"
               /\ TRUE
               /\ pc' = [pc EXCEPT ![self] = "c_put"]
               /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, 
                               alive, kills, where, dialing, orphan, stack, 
                               pconn, h, held, iter, msg, healthy, oconn >>

c_put(self) == /\ pc[self] = "c_put"
               /\ /\ pconn' = [pconn EXCEPT ![self] = held[self]]
                  /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "Put",
                                                           pc        |->  "c_after",
                                                           h         |->  h[self],
                                                           pconn     |->  pconn[self] ] >>
                                                       \o stack[self]]
               /\ h' = [h EXCEPT ![self] = FALSE]
               /\ pc' = [pc EXCEPT ![self] = "p_health"]
               /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, 
                               alive, kills, where, dialing, orphan, held, 
                               iter, msg, healthy, oconn >>

c_after(self) == /\ pc[self] = "c_after"
                 /\ held' = [held EXCEPT ![self] = NoConn]
                 /\ pc' = [pc EXCEPT ![self] = "c_top"]
                 /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, 
                                 alive, kills, where, dialing, orphan, stack, 
                                 pconn, h, iter, msg, healthy, oconn >>

Client(self) == c_top(self) \/ g_lock(self) \/ g_validate(self)
                   \/ g_validate_lock(self) \/ g_dial(self)
                   \/ g_dial_lock(self) \/ g_await(self)
                   \/ g_handoff_lock(self) \/ t_remove(self)
                   \/ t_drain(self) \/ t_forward(self) \/ c_use(self)
                   \/ c_put(self) \/ c_after(self)

o_wait(self) == /\ pc[self] = "o_wait"
                /\ orphan[(self - 100)]
                /\ \/ /\ nextId <= MaxIds
                      /\ oconn' = [oconn EXCEPT ![self] = nextId]
                      /\ nextId' = nextId + 1
                      /\ where' = [where EXCEPT ![oconn'[self]] = "held"]
                      /\ dialing' = dialing - 1
                      /\ pc' = [pc EXCEPT ![self] = "o_put"]
                      /\ UNCHANGED <<numOpen, waitQ, chan, orphan>>
                   \/ /\ numOpen' = numOpen - 1
                      /\ dialing' = dialing - 1
                      /\ IF waitQ # <<>>
                            THEN /\ Assert(chan[Head(waitQ)] = NoMsg, 
                                           "Failure of assertion at line 72, column 9 of macro called at line 245, column 13.")
                                 /\ chan' = [chan EXCEPT ![Head(waitQ)] = Retry]
                                 /\ waitQ' = Tail(waitQ)
                            ELSE /\ TRUE
                                 /\ UNCHANGED << waitQ, chan >>
                      /\ orphan' = [orphan EXCEPT ![(self - 100)] = FALSE]
                      /\ pc' = [pc EXCEPT ![self] = "o_wait"]
                      /\ UNCHANGED <<nextId, where, oconn>>
                /\ UNCHANGED << conns, closed, alive, kills, stack, pconn, h, 
                                held, iter, msg, healthy >>

o_put(self) == /\ pc[self] = "o_put"
               /\ /\ pconn' = [pconn EXCEPT ![self] = oconn[self]]
                  /\ stack' = [stack EXCEPT ![self] = << [ procedure |->  "Put",
                                                           pc        |->  "o_clear",
                                                           h         |->  h[self],
                                                           pconn     |->  pconn[self] ] >>
                                                       \o stack[self]]
               /\ h' = [h EXCEPT ![self] = FALSE]
               /\ pc' = [pc EXCEPT ![self] = "p_health"]
               /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, 
                               alive, kills, where, dialing, orphan, held, 
                               iter, msg, healthy, oconn >>

o_clear(self) == /\ pc[self] = "o_clear"
                 /\ orphan' = [orphan EXCEPT ![(self - 100)] = FALSE]
                 /\ pc' = [pc EXCEPT ![self] = "o_wait"]
                 /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, 
                                 alive, kills, where, dialing, stack, pconn, h, 
                                 held, iter, msg, healthy, oconn >>

Orphan(self) == o_wait(self) \/ o_put(self) \/ o_clear(self)

cl_loop == /\ pc[200] = "cl_loop"
           /\ Len(conns) > MinSize
           /\ \E k \in 1..Len(conns):
                /\ where' = [where EXCEPT ![conns[k]] = "discarded"]
                /\ conns' = [j \in 1..(Len(conns) - 1) |-> IF j < k THEN conns[j] ELSE conns[j + 1]]
           /\ numOpen' = numOpen - 1
           /\ IF waitQ # <<>>
                 THEN /\ Assert(chan[Head(waitQ)] = NoMsg, 
                                "Failure of assertion at line 72, column 9 of macro called at line 263, column 9.")
                      /\ chan' = [chan EXCEPT ![Head(waitQ)] = Retry]
                      /\ waitQ' = Tail(waitQ)
                 ELSE /\ TRUE
                      /\ UNCHANGED << waitQ, chan >>
           /\ pc' = [pc EXCEPT ![200] = "cl_loop"]
           /\ UNCHANGED << closed, nextId, alive, kills, dialing, orphan, 
                           stack, pconn, h, held, iter, msg, healthy, oconn >>

Cleaner == cl_loop

cs_start == /\ pc[201] = "cs_start"
            /\ \/ /\ TRUE
                  /\ UNCHANGED <<conns, numOpen, waitQ, closed, chan, where>>
               \/ /\ closed' = TRUE
                  /\ where' = [i \in Ids |-> IF where[i] = "idle" THEN "discarded" ELSE where[i]]
                  /\ numOpen' = numOpen - Len(conns)
                  /\ conns' = <<>>
                  /\ Assert(\A k \in 1..Len(waitQ) : chan[waitQ[k]] = NoMsg, 
                            "Failure of assertion at line 275, column 9.")
                  /\ chan' = [c \in Clients |-> IF \E k \in 1..Len(waitQ) : waitQ[k] = c
                                                THEN ErrMsg ELSE chan[c]]
                  /\ waitQ' = <<>>
            /\ pc' = [pc EXCEPT ![201] = "Done"]
            /\ UNCHANGED << nextId, alive, kills, dialing, orphan, stack, 
                            pconn, h, held, iter, msg, healthy, oconn >>

Closer == cs_start

e_loop == /\ pc[202] = "e_loop"
          /\ kills < MaxKills
          /\ \E i \in {j \in Ids : where[j] \in {"idle", "held"} /\ alive[j]}:
               alive' = [alive EXCEPT ![i] = FALSE]
          /\ kills' = kills + 1
          /\ pc' = [pc EXCEPT ![202] = "e_loop"]
          /\ UNCHANGED << conns, numOpen, waitQ, closed, chan, nextId, where, 
                          dialing, orphan, stack, pconn, h, held, iter, msg, 
                          healthy, oconn >>

Env == e_loop

Next == Cleaner \/ Closer \/ Env
           \/ (\E self \in ProcSet: Put(self))
           \/ (\E self \in Clients: Client(self))
           \/ (\E self \in {100 + c : c \in Clients}: Orphan(self))

Spec == /\ Init /\ [][Next]_vars
        /\ \A self \in Clients : WF_vars(Client(self)) /\ WF_vars(Put(self))
        /\ \A self \in {100 + c : c \in Clients} : WF_vars(Orphan(self)) /\ WF_vars(Put(self))
        /\ WF_vars(Cleaner)
        /\ WF_vars(Closer)

\* END TRANSLATION

\* Liveness: a queued waiter is eventually dequeued (no lost wakeup).
WaiterServed == \A c \in Clients :
    (\E k \in 1..Len(waitQ) : waitQ[k] = c) ~> ~(\E k \in 1..Len(waitQ) : waitQ[k] = c)

\* Liveness: every client finishes all its rounds.
ClientsFinish == <>(\A c \in Clients : pc[c] = "Done")
=============================================================================
