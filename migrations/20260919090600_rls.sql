-- +goose Up
-- Изоляция тенантов на уровне БД (§12.1, ADR-005).
--
-- Роли создаёт init-скрипт кластера (deploy/postgres/initdb/10-roles.sh), потому
-- что BYPASSRLS требует суперпользователя, а миграции идут от владельца схемы.
-- Здесь только политики и права; если ролей нет, гранты пропускаются.

-- +goose StatementBegin
DO $$
DECLARE
    t text;
    tenant_tables constant text[] := ARRAY[
        'users', 'sessions', 'warehouses', 'telegram_link_codes',
        'categories', 'suppliers', 'items', 'supplier_items',
        'stock_counts', 'stock_count_lines', 'stock_movements', 'stock_balances',
        'purchase_orders', 'purchase_order_lines',
        'daily_consumption', 'forecasts', 'forecast_metrics', 'item_status',
        'notifications', 'outbox', 'audit_log'
    ];
BEGIN
    FOREACH t IN ARRAY tenant_tables LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        -- Без заданного app.tenant_id current_setting возвращает NULL,
        -- сравнение даёт NULL, и политика не пропускает ни одной строки.
        EXECUTE format($sql$
            CREATE POLICY tenant_isolation ON %I
                USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
                WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
        $sql$, t);
    END LOOP;
END$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- У самих тенантов ключ изоляции — собственный id.
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenants
    USING (id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (id = nullif(current_setting('app.tenant_id', true), '')::uuid);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'zapas_app') THEN
        GRANT USAGE ON SCHEMA public TO zapas_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO zapas_app;

        -- ADR-002: журнал движений только дописывается. Право на UPDATE и DELETE
        -- отозвано, поэтому «поправить вчерашнее списание» физически невозможно.
        REVOKE UPDATE, DELETE ON stock_movements FROM zapas_app;

        -- Тенанты заводит и удаляет обслуживающая роль; приложение только читает
        -- свой и меняет настройки (часовой пояс, тихие часы, clock_offset).
        REVOKE DELETE ON tenants FROM zapas_app;

        -- Журнал аудита не переписывается задним числом.
        REVOKE UPDATE, DELETE ON audit_log FROM zapas_app;

        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO zapas_app;
    END IF;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'zapas_maint') THEN
        -- Обслуживающая роль (BYPASSRLS) нужна там, где тенант ещё неизвестен:
        -- вход по email, разбор сессии, обход тенантов ночными задачами,
        -- удаление истёкших песочниц.
        GRANT USAGE ON SCHEMA public TO zapas_maint;
        GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO zapas_maint;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO zapas_maint;
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    t text;
BEGIN
    FOR t IN
        SELECT tablename FROM pg_tables
        WHERE schemaname = 'public' AND rowsecurity
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
    END LOOP;
END$$;
-- +goose StatementEnd
