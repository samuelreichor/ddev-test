#!/usr/bin/env sh

# This uses /bin/sh, so it doesn't initialize profile/bashrc/etc

# ddev-webserver healthcheck

set -e

sleeptime=59

# Make sure that phpstatus, mounted code, webserver and mailpit
# are working.
# Since docker doesn't provide a lazy period for startup,
# we track health. If the last check showed healthy
# as determined by existence of /tmp/healthy, then
# sleep at startup. This requires the timeout to be set
# higher than the sleeptime used here.
if [ -f /tmp/healthy ]; then
    printf "container was previously healthy, so sleeping %s seconds before continuing healthcheck... " ${sleeptime}
    sleep ${sleeptime}
fi

# Shutdown the supervisor if one of the critical processes is in the FATAL state.
# One supervisorctl call for all of them, since each call takes over 100ms.
supervisor_status=$(supervisorctl status php-fpm nginx apache2 2>/dev/null || true)
for service in php-fpm nginx apache2; do
  if printf "%s\n" "${supervisor_status}" | grep -q "^${service}[[:space:]].*FATAL"; then
    printf "%s:FATAL " "${service}"
    supervisorctl shutdown
    # PID 1 doesn't exit when supervisord does, so stop the container to fail fast
    kill 1
  fi
done

phpstatus="false"
htmlaccess="false"
mailpit="false"

if ls /var/www/html >/dev/null; then
    htmlaccess="true"
    printf "/var/www/html:OK "
else
    printf "/var/www/html:FAILED "
fi

if curl -q --fail -s 127.0.0.1:8025 >/dev/null; then
    mailpit="true"
    printf "mailpit:OK "
else
    printf "mailpit:FAILED "
fi

# start.sh trusts the mkcert CA in the background
if [ -f /tmp/mkcert-install-pending ]; then
    printf "mkcert:PENDING "
    rm -f /tmp/healthy
    exit 1
fi

# If DDEV_WEBSERVER_TYPE is not set, use reasonable default
DDEV_WEBSERVER_TYPE=${DDEV_WEBSERVER_TYPE:-nginx-fpm}

if [ "${DDEV_WEBSERVER_TYPE}" = "generic" ] ; then
  if [ "${mailpit}" = "true" ] && [ "${htmlaccess}" = "true" ]; then
    touch /tmp/healthy
    exit 0
  else 
    exit 2
  fi
fi

if [ "${DDEV_WEBSERVER_TYPE#*-}" = "fpm" ]; then
  if curl -q --fail -s 127.0.0.1/phpstatus >/dev/null; then
    phpstatus="true"
    printf "phpstatus:OK "
  else
    printf "phpstatus:FAILED "
  fi
fi

if [ "${phpstatus}" = "true" ] && [ "${htmlaccess}" = "true" ] && [ "${mailpit}" = "true" ]; then
    touch /tmp/healthy
    exit 0
fi
rm -f /tmp/healthy

exit 1
