-- +goose Up
-- What an order is priced from. In a larger system customers, prices,
-- discounts and exchange rates would each be owned by a feature of their own;
-- the order feature only reads them.
CREATE TABLE customers (
    id         UUID PRIMARY KEY,
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE customer_groups (
    id   UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE customer_group_members (
    customer_id UUID NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    group_id    UUID NOT NULL REFERENCES customer_groups (id) ON DELETE CASCADE,
    PRIMARY KEY (customer_id, group_id)
);

-- One list price per item, in minor units of its currency.
CREATE TABLE item_prices (
    item_id     UUID PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    currency    CHAR(3) NOT NULL,
    unit_amount BIGINT  NOT NULL CHECK (unit_amount >= 0)
);

-- A NULL group_id applies to every customer, a NULL item_id to every item.
CREATE TABLE discounts (
    id           UUID PRIMARY KEY,
    name         TEXT NOT NULL,
    group_id     UUID REFERENCES customer_groups (id) ON DELETE CASCADE,
    item_id      UUID REFERENCES items (id) ON DELETE CASCADE,
    basis_points INT  NOT NULL CHECK (basis_points BETWEEN 1 AND 10000)
);

CREATE INDEX discounts_group_id_idx ON discounts (group_id);

-- One unit of base_currency costs rate units of quote_currency.
CREATE TABLE exchange_rates (
    base_currency  CHAR(3)         NOT NULL,
    quote_currency CHAR(3)         NOT NULL,
    rate           NUMERIC(20, 10) NOT NULL CHECK (rate > 0),
    PRIMARY KEY (base_currency, quote_currency)
);

CREATE TABLE orders (
    id           UUID PRIMARY KEY,
    customer_id  UUID        NOT NULL REFERENCES customers (id),
    currency     CHAR(3)     NOT NULL,
    total_amount BIGINT      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX orders_customer_id_idx ON orders (customer_id);

-- The line keeps the price and discount it was sold at, so later changes to
-- prices, discounts or rates do not rewrite past orders.
CREATE TABLE order_lines (
    order_id              UUID   NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    line_no               INT    NOT NULL,
    item_id               UUID   NOT NULL REFERENCES items (id),
    quantity              INT    NOT NULL CHECK (quantity > 0),
    unit_amount           BIGINT NOT NULL,
    discount_basis_points INT    NOT NULL,
    amount                BIGINT NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

-- +goose Down
DROP TABLE order_lines;
DROP TABLE orders;
DROP TABLE exchange_rates;
DROP TABLE discounts;
DROP TABLE item_prices;
DROP TABLE customer_group_members;
DROP TABLE customer_groups;
DROP TABLE customers;
