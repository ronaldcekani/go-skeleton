-- Demo data for the order example: `make seed`. Safe to run more than once.
--
-- One wholesale customer, two priced items (one in EUR, one in USD), a 10%
-- discount for the wholesale group and a USD -> EUR rate.

INSERT INTO items (id, name, description) VALUES
    ('00000000-0000-7000-8000-0000000000a1', 'widget', 'priced in EUR'),
    ('00000000-0000-7000-8000-0000000000a2', 'gadget', 'priced in USD')
ON CONFLICT DO NOTHING;

INSERT INTO customers (id, name) VALUES
    ('00000000-0000-7000-8000-0000000000c1', 'Acme Wholesale')
ON CONFLICT DO NOTHING;

INSERT INTO customer_groups (id, name) VALUES
    ('00000000-0000-7000-8000-0000000000b1', 'wholesale')
ON CONFLICT DO NOTHING;

INSERT INTO customer_group_members (customer_id, group_id) VALUES
    ('00000000-0000-7000-8000-0000000000c1', '00000000-0000-7000-8000-0000000000b1')
ON CONFLICT DO NOTHING;

INSERT INTO item_prices (item_id, currency, unit_amount) VALUES
    ('00000000-0000-7000-8000-0000000000a1', 'EUR', 1000),
    ('00000000-0000-7000-8000-0000000000a2', 'USD', 333)
ON CONFLICT DO NOTHING;

INSERT INTO discounts (id, name, group_id, item_id, basis_points) VALUES
    ('00000000-0000-7000-8000-0000000000d1', 'wholesale 10%', '00000000-0000-7000-8000-0000000000b1', NULL, 1000)
ON CONFLICT DO NOTHING;

INSERT INTO exchange_rates (base_currency, quote_currency, rate) VALUES
    ('USD', 'EUR', 0.9)
ON CONFLICT DO NOTHING;
