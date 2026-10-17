CREATE TABLE public.notifications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id > 0),
    request_id UUID NOT NULL UNIQUE CHECK (request_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    order_id BIGINT NOT NULL CHECK (order_id > 0),
    recipient VARCHAR(64) NOT NULL CHECK (recipient COLLATE "C" ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    message TEXT NOT NULL CHECK (octet_length(message) BETWEEN 1 AND 1024),
    simulate_result TEXT NOT NULL CHECK (simulate_result IN ('success','failure')),
    status TEXT NOT NULL CHECK (status IN ('received','sent','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status = 'received' OR (simulate_result = 'success' AND status = 'sent') OR (simulate_result = 'failure' AND status = 'failed')),
    UNIQUE (id, status)
);
CREATE TABLE public.notification_sink_receipts (
    notification_id BIGINT PRIMARY KEY REFERENCES public.notifications(id),
    result TEXT NOT NULL CHECK (result IN ('sent','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (notification_id,result) REFERENCES public.notifications(id,status) DEFERRABLE INITIALLY DEFERRED
);
