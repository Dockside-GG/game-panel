CREATE OR REPLACE FUNCTION dockside_enqueue_webhook_deliveries()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.server_id IS NULL THEN
        RETURN NEW;
    END IF;

    INSERT INTO webhook_deliveries(
        id, destination_id, event_id, status, next_attempt_at
    )
    SELECT
        gen_random_uuid(), destination.id, NEW.id, 'queued', now()
    FROM webhook_destinations AS destination
    WHERE destination.server_id = NEW.server_id
      AND destination.enabled = true
      AND (
          jsonb_array_length(destination.event_filters) = 0
          OR destination.event_filters ? NEW.event_type
          OR destination.event_filters ? ('severity:' || NEW.severity)
      );
    RETURN NEW;
END;
$$;

CREATE TRIGGER activity_enqueue_webhooks
AFTER INSERT ON activity_events
FOR EACH ROW
EXECUTE FUNCTION dockside_enqueue_webhook_deliveries();

CREATE INDEX webhook_delivery_claim_idx
    ON webhook_deliveries(next_attempt_at, created_at)
    WHERE status IN ('queued', 'retrying');
