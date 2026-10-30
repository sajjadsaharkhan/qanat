-- A monotonic config generation. Every config change bumps it, and the
-- NOTIFY payload carries "<table>:<generation>". The gateway records the
-- generation its snapshot was read at, so anyone can tell whether a change
-- has reached the data plane (the acceptance driver waits on this).
CREATE TABLE config_generation (
    id  boolean PRIMARY KEY DEFAULT true CHECK (id),
    gen bigint NOT NULL
);
INSERT INTO config_generation (id, gen) VALUES (true, 1);

CREATE OR REPLACE FUNCTION notify_config() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g bigint;
BEGIN
    UPDATE config_generation SET gen = gen + 1 RETURNING gen INTO g;
    PERFORM pg_notify('qanat_config', TG_TABLE_NAME || ':' || g);
    RETURN NULL;
END $$;
