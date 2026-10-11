CREATE TABLE public.orders (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    item_code VARCHAR(64) NOT NULL CHECK (item_code COLLATE "C" ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    quantity INTEGER NOT NULL CHECK (quantity BETWEEN 1 AND 999),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
