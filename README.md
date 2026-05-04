# devops-worker

`devops-worker` 是一个 Web + Telegram 的排班审批服务：

- Web 页面创建用户、创建排班策略、查看历史排班。
- 提交排班策略后只生成待审批草稿，不直接生效。
- Telegram bot 向指定审批人发送 HTML 预览附件和“同意 / 拒绝”按钮。
- 审批通过后才原子更新正式排班，并写入历史快照。
- 审批拒绝不会更新正式排班。
- 通过文件锁和原子写入降低 Pod 滚动更新时的数据污染风险。

## 快速启动

```bash
cp .env.example .env
# 修改 .env 里的 BOT_TOKEN、GROUP_CHAT_IDS、APPROVER_USER_IDS
set -a
source .env
set +a

go mod tidy
go run .
```

然后打开：

```text
http://服务器IP:8080
```

默认 Web Basic Auth：

```text
admin / change_me
```

生产环境必须修改 `ADMIN_PASSWORD`。

## 关键环境变量

| 变量 | 说明 |
|---|---|
| `BOT_TOKEN` | Telegram bot token。为空时 Web 可运行，但无法发送审批和接收回调。 |
| `GROUP_CHAT_IDS` | 用于查询、提醒、群内审批通知的 Telegram 群 ID，逗号分隔。 |
| `GROUP_CHAT_TOPICS` | 可选，格式：`chatID:topicID,chatID:topicID`。 |
| `APPROVER_USER_IDS` | 指定审批人的 Telegram user ID，逗号分隔。只有这些用户点同意/拒绝才有效。 |
| `WEB_ADDR` | Web 监听地址，默认 `:8080`。 |
| `DATA_DIR` | 数据目录，默认 `./data`。容器部署时必须挂载持久卷。 |
| `APP_TIMEZONE` | 排班时区，默认 `Asia/Shanghai`。 |
| `ADMIN_USERNAME` | Web Basic Auth 用户名。 |
| `ADMIN_PASSWORD` | Web Basic Auth 密码。 |

## Telegram 设置

新 bot 需要在 BotFather 设置：

```text
/setprivacy -> Disable
/setjoingroups -> Enable
```

审批人如果要接收私聊审批附件，需要先给 bot 发送 `/start`。如果私聊发送失败，程序仍会尝试向 `GROUP_CHAT_IDS` 群发送审批消息。

## 数据目录

```text
data/
├── config/shifts.json
├── users/users.json
├── schedules/active.json
├── schedules/by_day/YYYY-MM-DD.json
├── approvals/pending/*.json
├── approvals/approved/*.json
├── approvals/rejected/*.json
├── previews/*.html
├── history/YYYY/MM/YYYY-MM-DD.json
├── locks/*.lock
└── meta/reminders.json
```

## Kubernetes / Docker 注意事项

如果使用 JSON 文件存储，推荐：

1. `DATA_DIR` 挂载持久卷。
2. 单副本运行，或者滚动策略使用 `Recreate`。
3. 如果必须多个 Pod，只有抢到 `data/locks/bot.lock` 的实例会启动 Telegram getUpdates 和定时提醒，其他实例仍可提供 Web 页面。
4. 所有正式写入都会经过 `data/locks/storage.lock` 和临时文件 + rename 原子写入。

示例建议：

```yaml
strategy:
  type: Recreate
replicas: 1
volumeMounts:
  - name: schedule-data
    mountPath: /app/data
```

## 班次定义

默认初始化四种班次：

- 早班：09:00-18:00
- 中班：15:00-24:00
- 晚班：00:00-09:00
- 正常班：09:00-18:00

可以修改：

```text
data/config/shifts.json
```

## Web 功能

- `/`：今日排班与当前正式版本。
- `/users`：用户管理。
- `/schedule`：创建某月、某周、某星期、某班次排班策略并提交审批。
- `/approvals`：审批记录。
- `/history?date=YYYY-MM-DD`：按天查阅历史排班。
- `/previews/<approval-id>.html`：审批预览 HTML。

