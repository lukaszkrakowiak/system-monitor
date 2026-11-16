# auditd — wymagane ustawienia

1. Reguły: skopiuj `server-monitor.rules` do `/etc/audit/rules.d/` i załaduj (`augenrules --load`).
2. Dostęp demona do logu bez root — w `/etc/audit/auditd.conf` ustaw:

   ```
   log_group = adm
   ```

   (użytkownik `monitor` należy do grupy `adm`, patrz `deploy/systemd/sysusers-server-monitor.conf`),
   a katalog `/var/log/audit` powinien mieć mode `0750` (root:adm). Następnie `systemctl restart auditd`
   (w niektórych dystrybucjach restart wymaga `service auditd restart`).
3. Demon śledzi `/var/log/audit/audit.log` i wykrywa rotację po zmianie inode
   (po rotacji dokańcza `audit.log.1`). Pozycja (inode + offset) jest zapisana w tabeli `audit_log_state`.
