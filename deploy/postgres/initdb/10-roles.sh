#!/bin/bash
# Создаёт роли кластера. Запускается один раз при инициализации тома postgres
# (и testcontainers), потому что BYPASSRLS может выдать только суперпользователь.
set -euo pipefail

: "${APP_DB_PASSWORD:=zapas_app}"
: "${MAINT_DB_PASSWORD:=zapas_maint}"
: "${OWNER_DB_PASSWORD:=zapas_owner}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
-- Владелец схемы: от него идут миграции. RLS на владельца не действует,
-- поэтому приложение под этой ролью не работает.
CREATE ROLE zapas_owner LOGIN PASSWORD '${OWNER_DB_PASSWORD}' NOBYPASSRLS;
GRANT ALL ON DATABASE "$POSTGRES_DB" TO zapas_owner;
ALTER SCHEMA public OWNER TO zapas_owner;

-- Роль приложения: не владеет таблицами и не обходит RLS (§12.1, п. 3).
CREATE ROLE zapas_app LOGIN PASSWORD '${APP_DB_PASSWORD}' NOBYPASSRLS;

-- Обслуживающая роль: вход по email, разбор сессии, обход тенантов
-- ночными задачами, удаление истёкших песочниц.
CREATE ROLE zapas_maint LOGIN PASSWORD '${MAINT_DB_PASSWORD}' BYPASSRLS;

-- Публичные права на схему отзываем: доступ выдаётся ролям явно в миграции.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO zapas_app, zapas_maint;
SQL
