"""Shared JetStream push-consumer plumbing for kb-service consumers.

Python counterpart of the Go message_bus Subscribe path
(pkg/adapters/nats/message_bus.go): every kb-service NATS consumer
binds to a durable push consumer with ManualAck, an AckWait long
enough for a full document parse, a MaxDeliver ceiling, and an
InProgress heartbeat (ack_wait/3) that renews the delivery lease
while the handler still runs. Aligned with the deploy contract
(component-contracts/nats.yaml: jetstream required, subjects
ani.tasks.kb.*) and the Go model-import-worker constants (AckWait
30m, MaxDeliver 3).

Ack semantics:
  - handler success        -> Ack   (message leaves the WorkQueue stream)
  - invalid payload        -> Ack   (poison pill swallowed; the durable
                               outbox row is the operator's source of
                               truth — same convention as the Go worker)
  - handler crash          -> neither Ack nor Nak — silent for ack_wait
                               redelivery (a Nak would redeliver
                               immediately and race the still-live task
                               lease / row-level guards; the consumers
                               deliberately deviate from the Go
                               handlerFunc Nak-on-error here)
  - handler still running  -> InProgress every ack_wait/3
"""
from __future__ import annotations

import asyncio
import logging
from typing import Any, Callable

from nats.js.api import (
    AckPolicy,
    ConsumerConfig,
    DeliverPolicy,
    RetentionPolicy,
    StorageType,
    StreamConfig,
)
from nats.js.errors import NotFoundError

logger = logging.getLogger(__name__)

# Stream capturing every task subject (ani.tasks.>). Created by the Go
# bootstrap (pkg/bootstrap/nats.go ensureStreams) in production; ensured
# here idempotently as a defensive local-dev path.
ANI_TASKS_STREAM = "ANI_TASKS"
ANI_TASKS_SUBJECTS = ["ani.tasks.>"]
ANI_TASKS_MAX_AGE = 24 * 3600  # seconds


async def ensure_ani_tasks_stream(js: Any) -> None:
    """Idempotently ensure the ANI_TASKS stream exists.

    Mirrors Go ensureStreams: WorkQueue retention (message removed once
    acked), file storage, 24h MaxAge, single replica. No-op when the
    stream is already there (production: created by the Go side first).
    """
    try:
        await js.stream_info(ANI_TASKS_STREAM)
        return
    except NotFoundError:
        pass
    await js.add_stream(
        StreamConfig(
            name=ANI_TASKS_STREAM,
            subjects=ANI_TASKS_SUBJECTS,
            retention=RetentionPolicy.WORK_QUEUE,
            max_age=ANI_TASKS_MAX_AGE,
            storage=StorageType.FILE,
            num_replicas=1,
        )
    )
    logger.info("jetstream: created stream %s (%s)", ANI_TASKS_STREAM, ANI_TASKS_SUBJECTS)


def push_consumer_config(
    *,
    durable: str,
    filter_subject: str,
    ack_wait: float,
    max_deliver: int,
    max_ack_pending: int,
) -> ConsumerConfig:
    """Build the ConsumerConfig for a kb-service durable push consumer.

    deliver_group == durable (nats-py requires queue == durable when both
    are given): a deliver group lets a restarted replica rebind to the
    consumer after a crash, instead of hitting "consumer is already
    bound" on the non-queue push binding.
    """
    return ConsumerConfig(
        durable_name=durable,
        deliver_group=durable,
        filter_subject=filter_subject,
        deliver_policy=DeliverPolicy.ALL,
        ack_policy=AckPolicy.EXPLICIT,
        ack_wait=ack_wait,
        max_deliver=max_deliver,
        max_ack_pending=max_ack_pending,
    )


async def subscribe_durable(
    js: Any,
    *,
    subject: str,
    durable: str,
    cb: Callable[..., Any],
    ack_wait: float,
    max_deliver: int,
    max_ack_pending: int,
) -> Any:
    """Bind to (creating if absent) a durable push consumer with ManualAck.

    Idempotent across restarts: nats-py reuses the server-side consumer
    config when the durable already exists, so the parameters only shape
    the first creation.
    """
    config = push_consumer_config(
        durable=durable,
        filter_subject=subject,
        ack_wait=ack_wait,
        max_deliver=max_deliver,
        max_ack_pending=max_ack_pending,
    )
    return await js.subscribe(
        subject,
        queue=durable,
        cb=cb,
        durable=durable,
        manual_ack=True,
        config=config,
    )


async def ack(msg: Any) -> None:
    """Ack; on failure the message redelivers after ack_wait (logged)."""
    try:
        await msg.ack()
    except Exception:  # noqa: BLE001
        logger.error("jetstream: ack failed after handler success", exc_info=True)


async def nak(msg: Any) -> None:
    """Nak for immediate redelivery (up to the consumer's MaxDeliver)."""
    try:
        await msg.nak()
    except Exception:  # noqa: BLE001
        logger.error("jetstream: nak failed after handler error", exc_info=True)


async def heartbeat_loop(msg: Any, interval: float) -> None:
    """Renew the JetStream delivery lease (InProgress) every interval.

    Started alongside a long-running handler and cancelled by the caller
    once the outcome (ack/nak) is decided. Mirrors the Go message_bus
    heartbeat goroutine (ticker at AckWait/3).
    """
    while True:
        await asyncio.sleep(interval)
        try:
            await msg.in_progress()
        except Exception:  # noqa: BLE001 — best-effort renewal
            logger.debug("jetstream: in_progress renewal failed", exc_info=True)
