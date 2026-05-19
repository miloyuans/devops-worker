# devops-worker

`devops-worker` 是一个 Web + Telegram 的排班审批服务。当前版本已经从“上传 xlsx 更新排班”升级为：

- Web 科技务实风格控制台。
- 首页默认展示当月日历，并默认选中今天；详情面板固定在右侧，不再放到日历下方。
- 点击日期时使用前端局部更新，不刷新页面；当天日期使用浅绿色，选中日期使用浅蓝色，避免状态冲突。
- 周六、周日使用浅灰色标记，日历区域缩小并左移，适配浏览器窗口高度。
- 排班设置页与首页保持同样的左侧日历 + 右侧详情布局。右侧改为完整月排班预览矩阵，每个用户占一行，可直接勾选用户、选择班次，并将左侧选中的一天或多天确认加入草稿。
- 历史查询页与首页保持同样风格，不再使用流式记录表格。
- 新增班次设置页，可维护早班、中班、晚班、正常班或自定义班次；班次默认启用，跨天状态自动判断，并支持班次时区设置，默认迪拜时区 Asia/Dubai。
- 修改班次开始/结束时间后，未来且尚未触发通知的排班会自动同步新时间并立即生效。
- 提交排班后只生成待审批草稿，不直接生效。
- Telegram bot 优先向配置群发送 HTML 预览附件，并用 tg://user?id=... @ 审批人；同时尝试私聊审批人。审批通过/拒绝后会同步更新所有已发送的审批消息窗口。
- 审批通过后才原子更新正式排班，并写入历史快照。审批不再强依赖提交时版本号，而是使用唯一事务 ID；确认审批时会基于当时最新正式排班合并变更。
- 审批拒绝不会更新正式排班。
- 工作提醒改为持久化通知任务队列：排班生效后自动生成通知任务，任务在班次开始前 30 分钟进入可消费状态，消费成功后才标记已通知；失败任务每 300 秒重试，避免错过精确分钟导致漏提醒。
- 每天 09:00 会自动向配置群发送当天排班明细日报，按早/中/晚/正常/休/年/病等班次分组，内容与首页排班明细保持一致；Telegram 工作提醒、排班查询、每日排班明细和审批通知都会展示日期对应的星期几。
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

默认管理员登录：

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
| `WORK_ORDER_URL` | Telegram 工作提醒里展示的排班工单/Web 页面地址，可为空；工作提醒会同时展示用户电话资料（如已填写）。 |
| `DAILY_REPORT_TIME` | 每日排班明细自动发送时间，默认 `09:00`，使用 `APP_TIMEZONE` 时区。 |
| `DATA_DIR` | 数据目录，默认 `./data`。容器部署时必须挂载持久卷。 |
| `APP_TIMEZONE` | Telegram 定时提醒的默认时区，默认 `Asia/Dubai`。Web 页面右上角可单独下拉选择展示时区，默认也是 `Asia/Dubai`。 |
| `ADMIN_USERNAME` | 管理员登录用户名。 |
| `ADMIN_PASSWORD` | 管理员登录密码。 |
| `SSO_ENABLED` | 是否启用 Keycloak/OIDC SSO，默认 false。 |
| `OIDC_ISSUER_URL` | Keycloak realm issuer URL，例如 `https://keycloak.example.com/realms/devops`。 |
| `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | Keycloak OIDC Client 配置。 |
| `OIDC_REDIRECT_URL` | 回调地址，例如 `https://dwork.abc.om/sso/callback`。 |
| `SSO_ADMIN_USERS` | SSO 管理员用户白名单，逗号分隔。 |
| `SSO_ADMIN_ROLES` | SSO 管理员角色白名单，逗号分隔。 |


## Keycloak / OIDC SSO

系统已支持通过 Keycloak 的 OIDC Authorization Code Flow 登录管理员账号。普通用户仍然无需密码即可查看默认排班和提交排班审批；只有命中 SSO 管理员用户或管理员角色的账号会被授予 `admin` 超级管理员权限。

建议 Keycloak Client 设置：

```text
Client ID: devops-worker
Access Type: confidential
Valid Redirect URI: https://dwork.abc.om/sso/callback
Web Origins: https://dwork.abc.om
```

环境变量示例：

```bash
SSO_ENABLED=false
OIDC_ISSUER_URL=https://keycloak.example.com/realms/devops
OIDC_CLIENT_ID=devops-worker
OIDC_CLIENT_SECRET=replace_me
OIDC_REDIRECT_URL=https://dwork.abc.om/sso/callback
OIDC_SCOPES="openid profile email"
SSO_ADMIN_USERS=admin@example.com,admin_username
SSO_ADMIN_ROLES=devops-worker-admin,admin
```

权限判断规则：

- `SSO_ADMIN_USERS` 可匹配 `sub`、`preferred_username`、`email`、`name`。
- `SSO_ADMIN_ROLES` 可匹配 Keycloak token 中的 `roles`、`groups`、`realm_access.roles`、`resource_access.*.roles`。
- 未命中管理员规则的 SSO 用户不会获得 admin 权限，会以普通用户身份进入。
- 本地 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 登录仍然保留，便于 SSO 故障时兜底。

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

默认初始化七种班次：

- 早班：09:00-18:00
- 中班：15:00-24:00
- 晚班：00:00-09:00
- 正常班：09:00-18:00
- 休息：00:00-23:59
- 年假：00:00-23:59
- 病假：00:00-23:59

可以在 Web 的 `/shifts` 页面维护，也可以直接修改：

```text
data/config/shifts.json
```

班次跨天状态不需要手动设置，服务会根据开始时间和结束时间自动判断。开始/结束时间支持精确到分钟。例如 `18:00-03:00` 或 `15:00-24:00` 会被识别为跨天。新增班次默认使用迪拜时区 `Asia/Dubai`，也可以在下拉框中选择其他时区。班次时间变更保存后，系统会自动扫描当前正式排班，更新未来且未触发通知的记录，并生成新的 `version-<revision>-<random>` 版本号。


## 排班草稿合并与并发审批

排班设置页的草稿遵循“日期 + 用户”唯一键：

- 同一用户同一天被多次设置班次时，最终提交只保留最后一次选择。
- 右侧排班预览和页面底部的本月草稿都会自动合并重复内容。
- 多个用户同时提交审批时，不会用提交时的版本强制阻断。审批通过时，后端会重新读取当前最新正式排班，再把本审批单里的变更合并进去。
- 如果两个审批单修改同一用户同一天，后审批通过的变更覆盖先通过的变更。


### 自动补休规则

提交审批前，后端会对草稿做一次自动补休计算：

- 如果某个用户在本月被设置过早班，系统会自动把该用户本月所有周六、周日设置为“休息”。
- 如果某个用户在本月被设置过正常班，系统会自动把该用户本月所有周日设置为“休息”。
- 用户显式自定义设置优先级最高。例如已经手动把某个周六设置为中班，则不会被自动休息覆盖。
- 自动补休会进入审批预览 HTML，审批人看到的是最终合并后的生效结果。

## 通知任务队列与已读状态

通知任务状态：

- 审批生效或正式排班更新后，系统会把需要通知的排班生成到 `meta/notification_tasks.json`。
- 每个待发送任务按 `排班日期 + 用户 + 群ID` 作为稳定归属键；当班次或开始时间变更时，会原地刷新任务内容和 `run_at`，避免旧时间任务残留。实际发送记录仍包含班次与开始时间，避免重复通知。
- 任务的 `run_at` 是班次开始时间前 30 分钟；到达时间后进入消费。
- 消费成功后写入 `meta/notifications.json`，首页显示为“已通知”。
- 发送失败的任务会保留为 `retry` 状态，每 300 秒继续消费，直到发送成功。
- 已删除、已禁用或不再存在的未来未发送任务会自动取消；仍存在但班次/时间变化的任务会自动刷新为最新排班。

通知状态：

- `已通知`：通知任务已经成功发送。
- `未通知`：任务尚未到消费时间，或仍在队列中等待重试。

已读状态：

- `已读`：值班人员点击了 Telegram 提醒消息里的“我已读”按钮。
- `未读`：尚未点击确认。

注意：Telegram Bot API 不提供普通群消息真实阅读回执，因此这里的“已读”是业务确认状态，不是 Telegram 客户端阅读回执。


## 每日排班明细日报

- 默认每天 `09:00` 按 `APP_TIMEZONE` 时区向 `GROUP_CHAT_IDS` 配置的群发送当天排班明细，标题会展示日期和星期几。
- 可通过 `DAILY_REPORT_TIME=09:00` 调整发送时间。
- 日报按班次分组，分组顺序为：早班、中班、晚班、正常班、休息、年假、病假、自定义班次。
- 每个班次分组会显示班次时间、人数，以及每个用户的通知状态、已读状态、TG 绑定状态和电话资料。
- 发送状态记录在 `data/meta/daily_reports.json`，Pod 重启不会重复发送当天已成功发送的日报。
- 如果 Telegram 发送失败，该群当天日报不会标记成功，系统会每 300 秒重试。

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
├── meta/notification_tasks.json
├── meta/notifications.json
└── meta/daily_reports.json
```

旧版本的 `meta/reminders.json` 仍会初始化保留；新版本提醒触发主要使用 `meta/notification_tasks.json` 作为任务队列，通知成功结果写入 `meta/notifications.json`。

正式排班文件 `schedules/active.json` 会保存 `version_id`，格式类似 `version-3-a1b2c3`。审批记录会保存唯一 `transaction_id`，审批记录页面会根据用户表自动把审批人的 Telegram ID 映射成用户名展示。

## Kubernetes / Docker 注意事项

如果使用 JSON 文件存储，推荐：

1. `DATA_DIR` 挂载持久卷。
2. 单副本运行，或者滚动策略使用 `Recreate`。
3. 如果必须多个 Pod，只有抢到 `data/locks/bot.lock` 的实例会启动 Telegram getUpdates 和通知任务队列消费，其他实例仍可提供 Web 页面。
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

## 用户/班次删除与排班清理策略

- 同一用户同一天只允许保留一个有效班次；系统在审批生效、启动修复、定时校准时都会自动归一化 `date + staff_id`。
- 管理员第一次点击用户或班次的“删除”按钮时，不会立即物理删除，而是自动改为禁用，并异步清理今天及未来、尚未触发通知的相关排班。
- 已经发生的历史排班和已经触发过通知的排班会保留，避免破坏审计和通知一致性。
- 已禁用的用户或班次再次点击“彻底删除”时，才会从配置文件中物理移除；后台仍会再次尝试清理未来未通知排班。
- 后台每 10 分钟会自动校准正式排班，清理未来未通知的禁用/已删除用户或班次排班，并同步最新用户名称、Telegram ID 和班次时间。


## 通知任务队列节能策略

- 系统会根据最新正式排班生成持久化通知任务，任务文件位于 `data/meta/notification_tasks.json`。
- 当天仍有 `pending`、`retry` 或 `sending` 任务时，Telegram 持锁实例每 300 秒扫描并消费一次。
- 如果检测到当天全部任务都已 `sent` 或 `cancelled`，调度器会停止 300 秒循环扫描，休眠到下一个本地日期。
- 如果当天发生审批通过、班次调整、用户/班次禁用清理、排班一致性校准等事件，系统会唤醒通知队列调度器，重新同步并消费新增或刷新的任务。
- 这样可以避免当天任务全部完成后继续空跑扫描，同时不会影响当天后续排班变更产生的新通知任务。

## Recent updates

- Shift codes are now generated automatically as unique random strings when creating a new shift. Operators no longer need to enter or maintain shift codes manually.
- Shifts now include a `notify_enabled` setting. Custom shifts can opt out of Telegram reminder notifications. Rest, annual leave, and sick leave default to no notification.
- Schedule calendar selection supports range picking: click a start date, then click an end date to automatically select the full range. Ctrl/Cmd-click can still be used for manual multi-select adjustments.

## Web UI 审批

审批页 `/approvals` 现在支持 Web UI 直接审批：

- 普通用户只能查看审批记录和 HTML 预览。
- 只有通过 `/login` 登录的 admin 超级管理员可以点击“通过生效”或“拒绝”。
- Web UI 审批通过后会立即合并到最新正式排班并生效，同时刷新通知任务队列。
- 如果该审批曾发送 Telegram 审批窗口，Web UI 审批完成后也会同步更新 Telegram 消息状态。

## SSO 用户信息展示

SSO 登录成功后，页面右上角会自动展示 Keycloak/OIDC 返回的用户信息：

- 优先展示 `name`
- 如果没有 `name`，依次使用 `given_name + family_name`、`preferred_username`、`email`、`sub`
- 如果返回了 `email`，会在角色标签下方附带展示
- 普通 SSO 用户显示为“普通用户”，超级管理员显示为“超级管理员”

本地 admin 登录仍显示本地管理员账号。退出会同时清理本地 admin 会话和 SSO 用户会话。

## 本次版本补充：SSO 强制登录与系统用户自动关联

- 当 SSO 配置在 `/sso-settings` 中启用后，业务页面不再允许匿名访问；未登录用户访问 `/`、`/schedule`、`/users`、`/approvals`、`/history` 等页面会自动跳转到 `/login`。
- `/login` 会显示 Keycloak SSO 登录入口；本地管理员登录保留为备用入口 `/login?local=1`。
- SSO 登录成功后，系统会自动根据 Keycloak claims 关联或创建系统用户：
  1. 优先按 `sub` 匹配 `sso_sub`；
  2. 其次按 `email` 匹配用户邮箱或历史 SSO 邮箱；
  3. 再按 `preferred_username` 或显示名匹配系统用户名；
  4. 如果找不到，则自动创建一个 `created_by=sso` 的启用用户。
- 系统用户会记录 `email`、`sso_provider`、`sso_sub`、`sso_username`、`sso_email`、`last_sso_login_at`，方便后续审计和排班人员数据关联。

## 分组合并工作通知

- 用户资料新增 `group_id` 分组字段，默认分组为启用状态的 `devops`。
- 用户管理页提供分组创建、更新、启用、禁用和删除入口。
- 通知任务队列会按 `日期 + 班次 + 用户分组 + 群ID` 合并同组同班次员工的工作提醒。
- 没有有效分组或分组被禁用的用户，会退回原来的独立工作提醒模板。
- 合并通知会在同一条 Telegram 消息中展示多名员工及电话，例如：

```text
⏰ 工作提醒
分组: devops
日期: 2026-05-19 周二

员工:
abc    ☎️ 000000
345    ☎️ 099999
班次: 中班
时间: 15:00

排班工单: https://zb.optlink.top

还有 30 分钟开始值班，请注意交接, 如需查阅变更请使用工单。
```

- 合并通知会为每名员工生成独立“我已读 <用户名>”按钮。
- 绑定了 Telegram User ID 的员工只能由本人点击对应按钮确认；未绑定 Telegram User ID 的员工按钮不做身份限制。
- 所有组员都确认后，消息按钮会更新为“全部已读”。
