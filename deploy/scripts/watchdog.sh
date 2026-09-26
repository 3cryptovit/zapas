#!/bin/bash
# Сторож прода (§9.4).
#
# Полноценный Prometheus с Grafana на машине с 961 МБ ОЗУ, которую делят
# пять проектов, стоил бы дороже, чем всё, что он наблюдает. Поэтому здесь
# проверяется то, что действительно ломается, а тревога уходит в того же
# Telegram-бота, что и заказы.
#
# Молчание — норма: сообщение приходит на переходе «работает → сломалось»
# и обратно, а не каждые пять минут.
set -uo pipefail

set -a
. /etc/zapas/zapas.env
set +a

STATE_DIR=/srv/zapas/var
STATE="$STATE_DIR/watchdog.state"
mkdir -p "$STATE_DIR"

# Повторное напоминание, если авария затянулась.
REMIND_AFTER=$((6 * 3600))

problems=()

# --- сервисы ---
for unit in zapas-api zapas-worker; do
  if ! systemctl is-active --quiet "$unit"; then
    problems+=("$unit не запущен")
  fi
done

# --- готовность приложения ---
ready=$(curl -s --max-time 10 "http://${APP_ADDR}/readyz" || true)
case "$ready" in
  *'"ready":true'*) ;;
  '') problems+=("API не отвечает на /readyz") ;;
  *)  problems+=("API не готов: $(echo "$ready" | head -c 200)") ;;
esac

# --- место на диске ---
# Диск общий: переполнение убьёт не только Zapas.
disk=$(df --output=pcent / | tail -1 | tr -dc '0-9')
if [ "${disk:-0}" -ge 90 ]; then
  problems+=("на диске занято ${disk}%")
fi

# --- память ---
mem=$(free -m | awk '/^Mem:/ {print $7}')
if [ "${mem:-999}" -lt 60 ]; then
  problems+=("свободно всего ${mem} МБ памяти")
fi

# --- метрики приложения ---
metric() {
  echo "$1" | awk -v n="$2" '$1 == n {v = $2} END {print (v == "" ? "" : v)}'
}

raw=$(curl -s --max-time 10 "http://${METRICS_ADDR}/metrics" || true)
if [ -z "$raw" ]; then
  problems+=("метрики недоступны на ${METRICS_ADDR}")
else
  pending=$(metric "$raw" zapas_outbox_pending)
  # Очередь уведомлений разгребается за минуты; полсотни висящих — затор.
  if [ -n "$pending" ] && [ "${pending%.*}" -ge 50 ]; then
    problems+=("в очереди уведомлений застряло ${pending%.*}")
  fi

  drift=$(metric "$raw" zapas_balance_drift_total)
  # Расхождение остатка с журналом — это ошибка в данных, а не нагрузка.
  if [ -n "$drift" ] && [ "${drift%.*}" -gt 0 ]; then
    problems+=("остатки разошлись с журналом: ${drift%.*}")
  fi
fi

# --- срок сертификата ---
# Отсутствие файла — тоже тревога: иначе неверный путь после смены
# домена выключает проверку молча, и о сертификате узнают посетители.
cert=/etc/letsencrypt/live/vitalness.ru/cert.pem
if [ ! -f "$cert" ]; then
  problems+=("не найден сертификат $cert")
else
  until_ts=$(date -d "$(openssl x509 -enddate -noout -in "$cert" | cut -d= -f2)" +%s)
  days=$(( (until_ts - $(date +%s)) / 86400 ))
  if [ "$days" -lt 10 ]; then
    problems+=("сертификат истекает через ${days} дн.")
  fi
fi

# --- свежесть бэкапа ---
latest=$(find /var/backups/zapas/daily -name 'zapas-*.sql.gz' -mtime -2 | head -1)
if [ -z "$latest" ]; then
  problems+=("свежего бэкапа нет уже более суток")
fi

# --- доклад ---
notify() {
  logger -t zapas-watchdog "$1"
  echo "$1"
  if [ -n "${ZAPAS_ALERT_CHAT_ID:-}" ] && [ -n "${TELEGRAM_BOT_TOKEN:-}" ]; then
    curl -s --max-time 10 -o /dev/null \
      "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
      --data-urlencode "chat_id=${ZAPAS_ALERT_CHAT_ID}" \
      --data-urlencode "text=$1"
  fi
}

was=$(cut -d' ' -f1 "$STATE" 2>/dev/null || echo ok)
since=$(cut -d' ' -f2 "$STATE" 2>/dev/null || echo 0)
now=$(date +%s)

if [ ${#problems[@]} -eq 0 ]; then
  if [ "$was" = "fail" ]; then
    notify "Zapas: всё снова работает."
  fi
  echo "ok $now" > "$STATE"
  exit 0
fi

text="Zapas: проблемы на проде"
for p in "${problems[@]}"; do
  text="$text
— $p"
done

if [ "$was" != "fail" ] || [ $((now - since)) -ge "$REMIND_AFTER" ]; then
  notify "$text"
  echo "fail $now" > "$STATE"
else
  # Повторять одно и то же каждые пять минут — верный способ
  # научить владельца не читать эти сообщения.
  logger -t zapas-watchdog "проблемы сохраняются, напоминание подавлено"
  echo "fail $since" > "$STATE"
fi

exit 1
