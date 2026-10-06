Hive v0.4.5 stops repeated upstream reconnects from amplifying publisher terminal attach/detach churn.

- Transient source failures use bounded backoff, while persistent adapter or protocol failures stop retrying for the affected viewer and publication generation without disconnecting healthy sources.
- Canceling a pending source connection preserves the publisher transport and bounds outstanding channel-open work.

Compatibility: verified with Herdr 0.9.0 through 0.9.3; future versions must retain generation 1 and the frozen required codecs.
Update Hive with `hive update --state-dir PATH` and publishing Bees to 0.4.3 for controller reuse and classified failure handling.
After resolving a persistent controller conflict, reconnect the viewer or publish a fresh generation; input and requests are never replayed.
