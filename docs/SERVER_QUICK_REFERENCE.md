# LKO FOSS Mattermost Server - Quick Reference

## Server Details

- **URL**: https://chat.lkofoss.club
- **Server IP**: 77.42.94.83
- **OS**: AlmaLinux 10.1 (x86_64)
- **Deployment Date**: February 4, 2026

## Admin Access

- **Admin Email**: admin@lkofoss.club
- **Admin Username**: admin
- **Admin Password**: LkoFoss2025!

## User Access

- **Total Users**: 22 LKO FOSS Community members
- **Default Password**: LkoFoss2025!
- **Username**: Part before @ in email (e.g., venkatesh@tablaster.dev → venkatesh)

### User List

1. venkatesh@tablaster.dev - Venkatesh Chaturvedi
2. aviraj2403@gmail.com - Aviraj Saxena
3. abhinav24shukla08@gmail.com - Abhinav Shukla
4. abnanyad999@gmail.com - Abhinandan Yadav
5. rajrohan1914@gmail.com - Rohan Raj Gupta
6. divagupta028@gmail.com - Diva Gupta
7. ay079396@gmail.com - Ashish Kumar Yadav
8. darkxide29@gmail.com - Riva Parvez
9. srivastavamanika19@gmail.com - manika
10. ansh@fossunited.org - Ansh Arora
11. dhruvigaur30@gmail.com - Dhruvi Gaur
12. vishal@fossunited.org - Vishal Arya
13. pranjal1772004verma@gmail.com - Pranjal Verma
14. viditmathur122@gmail.com - Vidit Mathur
15. jreilly1821@gmail.com - James Reilly
16. vabhavtripathi15@gmail.com - Vabhav Tripathi
17. afnan3081n@gmail.com - Mohd Afnan
18. nazishfatima210@gmail.com - NAZISH FATIMA
19. abhiraj2004singh@gmail.com - Abhiraj Singh
20. leefred3042u@gmail.com - Zeeshan Ahmad Alavi
21. khushikumari2392006@gmail.com - Khushi Kumari
22. niyabits@gmail.com - Niya

## Teams

1. **LKO FOSS** - Main community team
2. **Organizers** - Event organizers
3. **Contributors** - Active contributors
4. **Events** - Event coordination

## Channels

1. **General** - General discussion
2. **Random** - Random chatter
3. **Announcements** - Important announcements
4. **Events** - Event planning and updates
5. **Projects** - Project discussions
6. **Help** - Get help from the community
7. **Introductions** - Introduce yourself
8. **Resources** - Share learning resources
9. **Off Topic** - Non-tech discussions
10. **Tech Talks** - Technical discussions

## Server Management

### SSH Access
```bash
ssh root@77.42.94.83
# Use id_rsa key
```

### Docker Commands
```bash
# View containers
docker ps

# View logs
docker logs -f mattermost
docker logs -f mattermost-db

# Restart containers
docker compose restart

# Stop/Start
docker compose down
docker compose up -d
```

### Mattermost Management (mmctl)
```bash
# List users
docker exec mattermost mmctl --local user list

# Create user
docker exec mattermost mmctl --local user create \
  --email user@example.com \
  --username username \
  --password Password123 \
  --email-verified

# Make user admin
docker exec mattermost mmctl --local user update --system-admin username

# Reset password
docker exec mattermost mmctl --local user reset-password username --password NewPass123
```

### Nginx Management
```bash
# Test configuration
nginx -t

# Reload (after config changes)
systemctl reload nginx

# Restart
systemctl restart nginx

# View logs
tail -f /var/log/nginx/access.log
tail -f /var/log/nginx/error.log
```

### SSL Certificate
```bash
# Check certificate expiry
certbot certificates

# Renew manually (auto-renewal configured)
certbot renew

# Test renewal
certbot renew --dry-run
```

## Configuration Files

### On Server (77.42.94.83)
- Docker Compose: `/root/production-setup/docker-compose.yml`
- Nginx Config: `/etc/nginx/conf.d/mattermost.conf`
- SSL Certs: `/etc/letsencrypt/live/chat.lkofoss.club/`
- Users JSON: `/root/production-setup/users.json`
- Config Env: `/root/production-setup/config.env`

### In Repository
- Setup Script: `scripts/setup-production-mattermost.sh`
- User Population: `production-setup/populate-real-users.sh`
- Documentation: `docs/PRODUCTION_DEPLOYMENT_LEARNINGS.md`

## Important Notes

### Security
- ⚠️ All users have default password: `LkoFoss2025!`
- ✅ Users should change password on first login
- ✅ SSL certificate valid until May 5, 2026
- ✅ Auto-renewal configured via certbot

### Backups
- 📦 Docker volumes: `production-setup_mattermost_*`
- 📦 PostgreSQL data: `production-setup_mattermost_db`
- 📝 Config files: `/root/production-setup/`

### Monitoring
- API Health: `curl https://chat.lkofoss.club/api/v4/system/ping`
- Should return: `{"status":"OK"}`

## Common Tasks

### Add New User
```bash
ssh root@77.42.94.83
docker exec mattermost mmctl --local user create \
  --email newuser@example.com \
  --username newuser \
  --password LkoFoss2025! \
  --email-verified
```

### Backup Database
```bash
ssh root@77.42.94.83
docker exec mattermost-db pg_dump -U mmuser mattermost > mattermost_backup_$(date +%Y%m%d).sql
```

### Restore Database
```bash
ssh root@77.42.94.83
cat backup.sql | docker exec -i mattermost-db psql -U mmuser -d mattermost
```

### View System Console
1. Login as admin at https://chat.lkofoss.club
2. Click hamburger menu (top left)
3. Select "System Console"

## Next Steps

1. ✅ Mattermost deployed and running
2. ✅ 22 real users created
3. ⏳ Set up Matrix bridge (mirror mode)
4. ⏳ Configure OAuth with Auth0
5. ⏳ Migrate to Matrix Authentication Service
6. ⏳ Update documentation with learnings

## Support

- Mattermost URL: https://chat.lkofoss.club
- Email: admin@lkofoss.club
- Mattermost: @admin

## Technical Stack

- **OS**: AlmaLinux 10.1
- **Docker**: 29.2.1
- **Docker Compose**: 5.0.2 (plugin)
- **Nginx**: 2:1.26.3-1.el10
- **Certbot**: 4.2.0-1.el10_1
- **Mattermost**: Enterprise (running as Team)
- **PostgreSQL**: 15-alpine
- **SSL**: Let's Encrypt (expires May 5, 2026)

---

**Status**: ✅ Production Ready  
**Last Updated**: February 4, 2026
