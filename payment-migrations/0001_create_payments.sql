CREATE TABLE public.payments (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id > 0),
 request_id UUID NOT NULL UNIQUE CHECK (request_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 order_id BIGINT NOT NULL CHECK (order_id > 0),
 amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
 currency TEXT NOT NULL CHECK (currency = 'JPY'),
 simulate_result TEXT NOT NULL CHECK (simulate_result IN ('success','decline')),
 status TEXT NOT NULL CHECK (status IN ('received','succeeded','declined')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (status = 'received' OR (simulate_result = 'success' AND status = 'succeeded') OR (simulate_result = 'decline' AND status = 'declined'))
);
