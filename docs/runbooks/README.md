# Раннбуки

Инструкции на случай, когда что-то нужно сделать с работающим продом.
Написаны так, чтобы ими можно было воспользоваться в три часа ночи,
не вспоминая, как всё устроено.

| Документ | Когда открывать |
|---|---|
| [deploy.md](deploy.md) | Выкатить новую версию |
| [rollback.md](rollback.md) | Выкатили, стало хуже |
| [restore.md](restore.md) | Данные потеряны или испорчены |
| [sandbox-abuse.md](sandbox-abuse.md) | Демо кто-то использует не по назначению |

## Что где лежит

| | |
|---|---|
| Адрес | `https://zapas.85.198.64.102.nip.io` |
| Код приложения | `/srv/zapas/bin/app`, предыдущая версия — `app.prev` |
| Статика | `/srv/zapas/web` (кабинет), `/srv/zapas/landing` |
| Настройки и секреты | `/etc/zapas/zapas.env`, режим 600, владелец root |
| Пароли БД | `/etc/zapas/db.secrets` |
| Учётка владельца | `/etc/zapas/owner.creds` |
| Бэкапы | `/var/backups/zapas/{daily,weekly}` |
| Сервисы | `zapas-api`, `zapas-worker` |
| Таймеры | `zapas-backup` (04:00), `zapas-watchdog` (каждые 5 минут) |
| nginx | `/etc/nginx/sites-available/zapas.conf` |

## Важно про этот сервер

Машина общая: кроме Zapas на ней живут `coffeecraft`,
`vadim-lubov-wedding`, `vmeste-api`, `vmeste-web` и `wedding-photobot`,
причём у свадебного сайта настоящий домен и настоящие посетители.

Отсюда правила:

- nginx и PostgreSQL общие. Zapas добавляет свой сайт и свою базу,
  но никогда не трогает чужие конфиги и не перезапускает PostgreSQL.
- `systemctl reload nginx`, а не `restart`: перезапуск роняет все сайты
  на машине, перечитывание конфига — нет.
- Перед `reload` всегда `nginx -t`. Сломанный конфиг положит не только Zapas.
- 1 vCPU и 961 МБ ОЗУ на всех. Поэтому лимиты в юнитах (`MemoryMax`,
  `CPUQuota`) — не украшение: без них один процесс выдавит остальные.
- 1,9 ГБ свободного диска. Перед любой операцией, которая пишет много
  (дамп, восстановление, распаковка), посмотрите `df -h /`.

## Быстрая проверка, что всё живо

```bash
systemctl is-active zapas-api zapas-worker
curl -s https://zapas.85.198.64.102.nip.io/readyz
/srv/zapas/bin/watchdog.sh          # молчит и код 0 — всё в порядке
```
