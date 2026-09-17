# Selected-field read measurements (#120)

Runs 26 (ORM) and 27 (driver transfer) in `benchmarks.sqlite` contain five fresh-process samples for each case. The tests used Go 1.27.1, Apple M4 Pro, and TypeDB 3.13.0 under Colima. Run 25 is a preliminary ORM result without response-size metrics.

Each case reads 64 entities. The narrow model has one key attribute. The wide model has a key and eight 256-byte string attributes. A projected read selects only the key, IID, and concrete type.

| Model | Read | Encoded FFI bytes | JSON-equivalent result bytes | ORM Go B/op | ORM allocs/op | ORM time/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Narrow | Full | 2,821 | 3,393 | 7,968 | 353 | 2.88 ms |
| Narrow | Projected | 4,741 | 5,569 | 59,265 | 678 | 4.16 ms |
| Wide | Full | 139,013 | 140,609 | 157,133 | 1,394 | 4.49 ms |
| Wide | Projected | 4,613 | 5,441 | 59,257 | 678 | 4.10 ms |

The encoded FFI bytes count the MessagePack result buffers passed from Rust to Go. They are not network bytes. The JSON-equivalent byte metric serializes one raw driver result outside the timed loop. The ORM timed loop includes query construction, transaction open, query execution, result conversion, and transaction close. The driver benchmark uses the same schema, row counts, value widths, and fetch shapes. It counts FFI bytes through the existing private stream callback.

The narrow projection is slower and allocates more because it adds a type label and a field map. The wide projection cuts encoded FFI bytes by 97% and ORM Go allocation volume by 62%. Its mean ORM time improves by 9%. The timing difference is small and subject to host load.

The API is opt-in. No automatic threshold follows from these two shapes. A caller can use a narrow projection when the wide result size matters more than the added result-map cost.
