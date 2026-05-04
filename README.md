# devops-worker

`devops-worker` 是一个 Web + Telegram 的排班审批服务。当前版本已经从“上传 xlsx 更新排班”升级为：

- Web 科技务实风格控制台。
- 首页默认展示当月日历，并默认选中今天；详情面板固定在右侧，不再放到日历下方。
- 点击日期时使用前端局部更新，不刷新页面；当天日期使用浅绿色，选中日期使用浅蓝色，避免状态冲突。
- 周六、周日使用浅灰色标记，日历区域缩小并左移，适配浏览器窗口高度。
- 排班设置页与首页保持同样的左侧日历 + 右侧详情布局，可点击一个或多个日期后弹窗选择用户和班次。弹窗内支持本次草稿，多次编辑会自动按“日期 + 用户”合并，最新班次覆盖旧班次，最后统一提交一次审批。
- 历史查询页与首页保持同样风格，不再使用流式记录表格。
- 新增班次设置页，可维护早班、中班、晚班、正常班或自定义班次；班次默认启用，跨天状态自动判断，并支持班次时区设置，默认迪拜时区 Asia/Dubai。
- 修改班次开始/结束时间后，未来且尚未触发通知的排班会自动同步新时间并立即生效。
- 提交排班后只生成待审批草稿，不直接生效。
- Telegram bot 优先向配置群发送 HTML 预览附件，并用 tg://user?id=... @ 审批人；同时尝试私聊审批人。审批通过/拒绝后会同步更新所有已发送的审批消息窗口。
- 审批通过后才原子更新正式排班，并写入历史快照。审批不再强依赖提交时版本号，而是使用唯一事务 ID；确认审批时会基于当时最新正式排班合并变更。
- 审批拒绝不会更新正式排班。
- 上班前 30 分钟 Telegram 自动提醒，并带“我已读”按钮。
- Web 的“已读”状态来自值班人员点击“我已读”按钮；Telegram 不提供普通群消息真实阅读回执。
- 通过文件锁和原子写入降低 Pod 滚动更新时的数据污染风险。

## 快速启动

```bash
cp .env.example .env
# 修改 .env 里的 BOT_TOKEN、GROUP_CHAT_IDS、APPROVER_USER_IDS
set -a
source .env
set +a

CGO_ENABLED=0 go build -o devops-worker .
./devops-worker
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

## Web 页面

| 页面 | 说明 |
|---|---|
| `/` | 首页日历，默认当月，默认选中今天；右侧展示当天排班、通知状态、已读状态。 |
| `/schedule` | 排班设置，日历点击多选日期，弹窗选择用户和班次，先加入草稿，最终统一提交 Telegram 审批。 |
| `/shifts` | 班次设置，维护班次编码、名称、简称、开始时间、结束时间、时区和启用状态；跨天自动判断。 |
| `/users` | 用户管理，维护用户名/别名和 Telegram User ID。 |
| `/approvals` | 审批记录。 |
| `/history?date=YYYY-MM-DD` | 以日历页方式查阅历史排班。 |
| `/previews/<approval-id>.html` | 审批预览 HTML。 |

## 班次定义

默认初始化四种班次：

- 早班：09:00-18:00
- 中班：15:00-24:00
- 晚班：00:00-09:00
- 正常班：09:00-18:00

可以在 Web 的 `/shifts` 页面维护，也可以直接修改：

```text
data/config/shifts.json
```

班次跨天状态不需要手动设置，服务会根据开始时间和结束时间自动判断。例如 `18:00-03:00` 或 `15:00-24:00` 会被识别为跨天。新增班次默认使用迪拜时区 `Asia/Dubai`，也可以在下拉框中选择其他时区。班次时间变更保存后，系统会自动扫描当前正式排班，更新未来且未触发通知的记录，并生成新的 `version-<revision>-<random>` 版本号。


## 排班草稿合并与并发审批

排班设置页的草稿遵循“日期 + 用户”唯一键：

- 同一用户同一天被多次设置班次时，最终提交只保留最后一次选择。
- 弹窗内的本次草稿和页面底部的本月草稿都会自动合并重复内容。
- 多个用户同时提交审批时，不会用提交时的版本强制阻断。审批通过时，后端会重新读取当前最新正式排班，再把本审批单里的变更合并进去。
- 如果两个审批单修改同一用户同一天，后审批通过的变更覆盖先通过的变更。

## 通知与已读状态

通知状态：

- `已通知`：系统已发送上班提醒。
- `未通知`：尚未到提醒时间，或未发送成功。

已读状态：

- `已读`：值班人员点击了 Telegram 提醒消息里的“我已读”按钮。
- `未读`：尚未点击确认。

注意：Telegram Bot API 不提供普通群消息真实阅读回执，因此这里的“已读”是业务确认状态，不是 Telegram 客户端阅读回执。

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
└── meta/notifications.json
```

旧版本的 `meta/reminders.json` 仍会初始化保留，但新版本通知状态主要使用 `meta/notifications.json`。

正式排班文件 `schedules/active.json` 会保存 `version_id`，格式类似 `version-3-a1b2c3`。审批记录会保存唯一 `transaction_id`，审批记录页面会根据用户表自动把审批人的 Telegram ID 映射成用户名展示。

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
