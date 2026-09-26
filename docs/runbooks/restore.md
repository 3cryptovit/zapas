# Восстановление из бэкапа

Бэкап, который ни разу не восстанавливали, бэкапом не считается.
Здесь описан и боевой порядок, и учебный — второй стоит повторять
хотя бы раз в квартал.

## Что есть

Ежедневно в 04:00 таймер `zapas-backup` снимает сжатый дамп:

```
/var/backups/zapas/daily/zapas-ГГГГ-ММ-ДД.sql.gz    7 последних
/var/backups/zapas/weekly/zapas-ГГГГ-ММ-ДД.sql.gz   4 последних воскресных
```

Дамп снимается с `--clean --if-exists`, то есть восстанавливается в
непустую базу без ручной подготовки. Если дамп получился подозрительно
маленьким, скрипт завершается с ошибкой — пустой файл не должен тихо
вытеснить хорошую копию.

Проверить, что бэкапы идут:

```bash
systemctl list-timers zapas-backup --no-pager
ls -la /var/backups/zapas/daily/
journalctl -u zapas-backup -n 20 --no-pager
```

## Учебное восстановление

Безопасно, боевую базу не трогает. Именно этим порядком проверено,
что дампы рабочие.

```bash
DUMP=$(ls -1t /var/backups/zapas/daily/*.sql.gz | head -1)

sudo -u postgres psql -qtAc 'CREATE DATABASE zapas_drill OWNER zapas_owner'
gunzip -c "$DUMP" | sudo -u postgres psql -q -d zapas_drill 2>/tmp/drill.err

# Ошибок быть не должно
grep -c '^ERROR' /tmp/drill.err

# Данные на месте
sudo -u postgres psql -qtA -d zapas_drill -c "
  select 'позиций=' || (select count(*) from items)
      || ' движений=' || (select count(*) from stock_movements)
      || ' таблиц с RLS=' || (select count(*) from pg_class where relrowsecurity)"

sudo -u postgres psql -qtAc 'DROP DATABASE zapas_drill'
```

Последняя строка проверки важна отдельно: изоляция организаций держится
на политиках RLS, и если они не восстановились, база выглядит целой,
но данные одной кофейни станут видны другой.

Перед учебным прогоном посмотрите `df -h /` — на диске меньше двух
гигабайт свободного места.

## Боевое восстановление

Порядок именно такой. Каждый шаг важен.

**1. Остановить запись.** Пока приложение пишет, восстановление
бессмысленно.

```bash
systemctl stop zapas-api zapas-worker
```

**2. Снять дамп текущего состояния.** Даже испорченного: в нём есть
данные, которых нет в ночной копии.

```bash
sudo -u postgres pg_dump --clean --if-exists zapas | gzip -9 > /var/backups/zapas/before-restore.sql.gz
```

**3. Выбрать копию.**

```bash
ls -la /var/backups/zapas/daily/ /var/backups/zapas/weekly/
```

Берите последнюю копию **до** момента порчи, а не просто свежую.

**4. Восстановить.**

```bash
gunzip -c /var/backups/zapas/daily/zapas-ГГГГ-ММ-ДД.sql.gz \
  | sudo -u postgres psql -d zapas 2>/tmp/restore.err
grep '^ERROR' /tmp/restore.err | head
```

**5. Проверить до запуска.**

```bash
sudo -u postgres psql -qtA -d zapas -c "
  select count(*) from items;
  select count(*) from stock_movements;
  select count(*) from pg_class where relrowsecurity;"
```

**6. Запустить и убедиться.**

```bash
systemctl start zapas-api zapas-worker
sleep 3
curl -s https://vitalness.ru/zapas/readyz
```

**7. Сказать владельцу**, на какой момент откатились и что именно
потеряно. Это не формальность: если человек не знает, что приёмка
вчерашней поставки пропала, он её не повторит, и остатки разойдутся
с реальностью.

## Сколько данных теряется

Дамп ночной, значит в худшем случае теряется день работы. Для кофейни
с 1–5 точками это приемлемо, и осознанно: непрерывное архивирование
WAL на машине с 1,9 ГБ свободного диска не поместится.

Если требование изменится, первым шагом будет не WAL, а выгрузка
дампов за пределы этого сервера — сейчас они лежат на том же диске,
что и база, и потеря диска означает потерю и того, и другого.
