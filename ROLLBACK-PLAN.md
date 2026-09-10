# v2.0.0 回滚预案

> 状态: v2.0.0 已于 2026-09-10 切换上线,本预案在切换后 1-2 周内有效
> 备份: data-backup-v1.0.4-202609101534/ (切换前的生产库完整备份)

## 回滚步骤

```bash
cd /home/yuan/docker/octopus-new
docker compose down
sudo rm -f data/data.db data/data.db-shm data/data.db-wal
sudo cp data-backup-v1.0.4-202609101534/data/data.db data/data.db
# 修改 docker-compose.yml: image: raynmy/octopus:v1.0.6 (本地与 Docker Hub 均有)
docker compose up -d
```

## 注意

- 回滚镜像用 v1.0.6(本地已缓存,Docker Hub 可拉;备份库是 v1 结构,任何 v1.0.x 可读)
- 回滚后 v2 期间产生的数据(渠道变更/新日志)会丢失,需人工评估
- v2 库如需保留,切换前已另存于 data-v2/data.db
