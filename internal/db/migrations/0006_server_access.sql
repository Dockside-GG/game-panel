INSERT INTO permissions(name, description) VALUES
    ('server.webhooks.manage', 'Manage webhook destinations')
ON CONFLICT DO NOTHING;
