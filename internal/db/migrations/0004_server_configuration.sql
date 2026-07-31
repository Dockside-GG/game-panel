ALTER TABLE servers
    ADD COLUMN startup_override text,
    ADD CONSTRAINT servers_startup_override_length
        CHECK (startup_override IS NULL OR length(startup_override) BETWEEN 1 AND 32768);
