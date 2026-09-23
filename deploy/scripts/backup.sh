#!/bin/bash
# Резервная копия базы Zapas (§13.5).
#
# Хранение: 7 ежедневных и 4 недельных копии. Недельной считается
# копия, снятая в воскресенье.
#
# Бэкап, который ни разу не восстанавливали, не считается бэкапом —
# порядок проверки в docs/runbooks/restore.md.
set -euo pipefail

BACKUP_DIR=/var/backups/zapas
DAILY="$BACKUP_DIR/daily"
WEEKLY="$BACKUP_DIR/weekly"

mkdir -p "$DAILY" "$WEEKLY"
chmod 700 "$BACKUP_DIR" "$DAILY" "$WEEKLY"

STAMP=$(date +%F)
FILE="$DAILY/zapas-$STAMP.sql.gz"

# --clean --if-exists: восстановление в непустую базу не требует
# ручной подготовки.
sudo -u postgres pg_dump --clean --if-exists zapas | gzip -9 > "$FILE.tmp"
mv "$FILE.tmp" "$FILE"
chmod 600 "$FILE"

# Воскресная копия дополнительно уезжает в недельные.
if [ "$(date +%u)" = "7" ]; then
  cp "$FILE" "$WEEKLY/zapas-$STAMP.sql.gz"
fi

# Чистка: 7 дневных, 4 недельных.
#
# Пустой каталог — штатная ситуация первого запуска, а не ошибка:
# ls на нём возвращает 2, и под pipefail это валило весь бэкап
# уже после того, как дамп благополучно снят.
rotate() {
  local dir="$1" keep="$2"
  # Имена датированные, поэтому обратная сортировка по имени
  # ставит свежие первыми без обращения к mtime.
  find "$dir" -maxdepth 1 -name 'zapas-*.sql.gz' | sort -r |
    tail -n "+$((keep + 1))" | xargs -r rm --
}

rotate "$DAILY" 7
rotate "$WEEKLY" 4

SIZE=$(du -h "$FILE" | cut -f1)
echo "backup ok: $FILE ($SIZE)"

# Пустой дамп — это провал, а не успех: проверяем размер.
MIN_BYTES=10240
ACTUAL=$(stat -c %s "$FILE")
if [ "$ACTUAL" -lt "$MIN_BYTES" ]; then
  echo "backup suspiciously small: $ACTUAL bytes" >&2
  exit 1
fi
