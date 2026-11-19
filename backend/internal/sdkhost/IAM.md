# Internal IAM integration (not serving-enabled)

Omitting `WithIAM` preserves legacy SDK behavior. Explicitly supplying a nil
authority denies protected operations; it is not a deployment feature flag.
Serving still omits the option. Jobs and schedules need their separate staged services.
Spans permit only explicit execution-context `spans.emit` on `own_execution`,
with a required live current-artifact observer. Admission hands an owned envelope
to the nonblocking optional writer; nil/unavailable/full/shutting-down writers
shed without synchronous fallback. Identity comes from the original host dispatch,
not caller-visible Exec fields. Persistence/retry never acquires IAM or reauthorizes.
The same admission gate binds the deciding owner UUID and policy revision into
private, by-value queue metadata. Legacy spans carry no IAM decision. This
provenance survives retry/persist-or-shed processing but is not a database column,
SDK result field or durable audit record; it cannot authorize another emission.
This is not runtime acceptance or boot activation. Other unsupported operations remain denied in the
partial IAM path, so it must not be enabled for production.

KV uses fresh observed admission and synchronous bounded transactions, with
whole-batch authorization before accesses and literal-prefix cursor binding.
Buffered and streaming calls additionally require `WithIAMTargetState`: a
trusted bounded current/acquired-worker observer sharing the same admission
gate. Policy presence or a fresh target-ID lookup is not current artifact proof.
Real pools still provide no such attested binding.

Streaming open registers the caller/target edge before pool waiting and admits
the actual acquired worker before execution binding or dispatch. Openings and
handles remain lease/channel-owned. Registration lives until terminal completion,
owned close, cancellation or failure; revocation cancels callee contexts and
cleanup runs outside the IAM gate and local session locks.

Each head, chunk, completion or error rechecks fresh caller lease, current
policies, registration and acquired-target artifact state before handoff.
Storage is allocated before admission; the gate callback only copies bounded
bytes. Frame reads, JSON/base64 work, teardown and protocol writes run outside it.
A handed-off result may physically arrive after a later revoke.

Dynamic open/next errors serialize their actual SDKError envelope first and
must fit the 256-KiB handoff bound. Oversized errors yield generic denial, not
an empty authorization token followed by an unbounded error message. Returned
errors are reconstructed solely from admitted owned bytes.

Tests cover wait/open/next cancellation, stale identities and artifacts,
pending/handle caps, buffered stream replies, lock-independent cleanup,
boundary-sized/escaped/Unicode error envelopes and compatibility. Successful
handoff fixtures use separately admitted host-owned test workers. Those checks
do not prove live pool attestation, real nested SDK runtime behavior, async
authorization, operator APIs, restore quarantine or coordinated serving cut-over.
