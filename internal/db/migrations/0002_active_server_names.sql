ALTER TABLE servers
    DROP CONSTRAINT servers_installation_id_name_key;

CREATE UNIQUE INDEX servers_active_name_unique_idx
    ON servers (installation_id, lower(name))
    WHERE deleted_at IS NULL;
