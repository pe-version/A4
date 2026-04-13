# Architecture Review — A4

## Core Services

The core services of this system are a sensor service, currently simulating input but extensible to actual physical sensors providing data; and an alert service, allowing for the creation, editing, and deletion of alerts and the triggering of said alerts with relevant logging and messaging when the appropriate conditions/thresholds are breached.

## Bottlenecks

The RabbitMQ fanout exchange is an obvious bottleneck, as the alert service could fall behind in consumption if it is slower than the sensor service, and in that case the queue would back up. The write path in the sensor service could be a bottleneck under heavy load since a synchronous SQLite write is performed before publishing. Notably, the fanout exchange feeds four alert service consumers — Python and Go implementations each subscribing independently — which was a consequence of prior assignments requiring dual-language implementation rather than a deliberate architectural choice. The resulting redundancy is nevertheless a useful property, as it provides fault tolerance if one implementation falls behind or fails.

## Stateful vs Stateless

SQLite renders both services stateful. The HTTP handlers are stateless, but the persistence layer isn't, which affects scaling.

## Scaling Concerns

SQLite is the main scaling concern because it's file-based single-writer, so it can't scale horizontally without file locking issues.

## What I Would Refactor

SQLite was chosen for pragmatic reasons — the assignment constraints and explicit instructor confirmation of its appropriateness for this context — but even at that initial decision point it was clear that a production system would require something more robust and parallelizable, such as Postgres, whether as a full replacement or via a shared volume with WAL mode enabled.

## Failure Concerns

One serious concern is that of cascading failures of services. In order to mitigate that, if the sensor service is temporarily down, a circuit breaker is configured to return an error immediately to the alert service's synchronous HTTP calls after 5 consecutive failures of the sensor service to respond with a reset to half-open after 30 seconds to allow for one probe request. This mitigates the risk of having a failure of the sensor service block the alert service. The circuit breaker does NOT cover in-flight alerts during the open state, which would be lost. For a sensor service, fail-fast is better than indefinite blocking, and a guarantee of every delivery isn't too worrisome, as an alert will be triggered more than once if the relevant alert condition persists. Additionally, it doesn't itself cover the growth of the queue backlog. As for the asynchronous pipeline, the concern of blocking is irrelevant, so no such circuit breaker is necessary.