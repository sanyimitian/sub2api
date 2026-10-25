-- Change only future inserts. Existing account values and shadow inheritance stay intact.
CREATE OR REPLACE FUNCTION public.enforce_openai_long_context_billing_extra()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    parent_effective_value JSONB;
BEGIN
    IF NEW.platform IS DISTINCT FROM 'openai' THEN
        RETURN NEW;
    END IF;

    NEW.extra := COALESCE(NEW.extra, '{}'::jsonb);
    IF NEW.parent_account_id IS NOT NULL AND NEW.quota_dimension = 'spark' THEN
        SELECT CASE
            WHEN parent.platform IS DISTINCT FROM 'openai' THEN 'false'::jsonb
            WHEN NOT (COALESCE(parent.extra, '{}'::jsonb) ? 'openai_long_context_billing_enabled') THEN 'false'::jsonb
            WHEN jsonb_typeof(parent.extra->'openai_long_context_billing_enabled') = 'boolean'
                THEN parent.extra->'openai_long_context_billing_enabled'
            ELSE 'false'::jsonb
        END
        INTO parent_effective_value
        FROM accounts AS parent
        WHERE parent.id = NEW.parent_account_id;

        NEW.extra := jsonb_set(
            NEW.extra,
            '{openai_long_context_billing_enabled}',
            COALESCE(parent_effective_value, 'false'::jsonb),
            true
        );
    ELSIF NOT (NEW.extra ? 'openai_long_context_billing_enabled')
        AND TG_OP = 'UPDATE'
        AND OLD.platform = 'openai'
        AND jsonb_typeof(OLD.extra->'openai_long_context_billing_enabled') = 'boolean' THEN
        NEW.extra := jsonb_set(
            NEW.extra,
            '{openai_long_context_billing_enabled}',
            OLD.extra->'openai_long_context_billing_enabled',
            true
        );
    ELSIF NOT (NEW.extra ? 'openai_long_context_billing_enabled') THEN
        NEW.extra := jsonb_set(
            NEW.extra,
            '{openai_long_context_billing_enabled}',
            CASE WHEN TG_OP = 'INSERT' THEN 'true'::jsonb ELSE 'false'::jsonb END,
            true
        );
    END IF;

    IF jsonb_typeof(NEW.extra->'openai_long_context_billing_enabled') IS DISTINCT FROM 'boolean' THEN
        RAISE EXCEPTION 'openai_long_context_billing_enabled must be a boolean'
            USING ERRCODE = '22023';
    END IF;
    RETURN NEW;
END;
$$;
