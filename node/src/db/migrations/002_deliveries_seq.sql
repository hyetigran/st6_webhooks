-- ADR-0007 (docs/adr/): deliveries.seq gives same-endpoint delivery rows a
-- reliable relative order that doesn't depend on created_at. Postgres's
-- now() is transaction-stable — every statement inside one transaction sees
-- the identical value — so a multi-row same-endpoint insert (replay's bulk
-- expansion, ADR-0005) would otherwise give every new row an identical
-- created_at, leaving their relative order to break arbitrarily wherever it
-- ties. Expansion (ADR-0004) never hit this: one event's expansion inserts
-- at most one delivery per endpoint, so there's never a same-endpoint tie.

ALTER TABLE deliveries ADD COLUMN seq BIGSERIAL;

DROP INDEX idx_deliveries_endpoint_pending;
CREATE INDEX idx_deliveries_endpoint_pending ON deliveries (endpoint_id, seq)
  WHERE state IN ('pending', 'in_flight');
