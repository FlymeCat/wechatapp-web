# wechatapp-web

Gin (Go) 实现的**图片换背景** HTTP 服务：上传一张图片，先用 [remove.bg](https://www.remove.bg/api) API 去掉原背景，再把抠出的前景合成到新的背景上（纯色或另一张背景图）。自带 Swagger 接口文档、Makefile 与 Docker 化支持。

实现逻辑参考自 remove.bg 官方的 Python 示例：

```python
import requests
response = requests.post(
    'https://api.remove.bg/v1.0/removebg',
    files={'image_file': open('/path/to/file.jpg', 'rb')},
    data={'size': 'auto'},
    headers={'X-Api-Key': 'INSERT_YOUR_API_KEY_HERE'},
)
```

## 快速开始

```bash
# 1. 配置 API Key
export REMOVE_BG_API_KEY=your-api-key-here     # https://www.remove.bg/api 注册获取

# 2. 启动（开发模式）
make run

# 或构建二进制
make build && ./bin/wechatapp-server
```

启动后：

- 接口文档（Swagger UI）：<http://localhost:8080/swagger/index.html>
- 健康检查：<http://localhost:8080/healthz>

## 用户与认证（JWT）

服务内置账号体系：注册 / 登录签发 JWT，登出使 token 失效；支持信息查看与管理员人员管理。启动时自动播种初始管理员（`ADMIN_USERNAME` / `ADMIN_PASSWORD`，默认 `admin` / `admin123`，**部署前务必修改**）。

**鉴权方式**：登录接口返回的 `token` 放在请求头

```
Authorization: Bearer <token>
```

**角色**：`admin`（可管理所有用户）/ `user`（仅可查看、修改自己）。

### 认证接口（公开部分）

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| `POST` | `/api/v1/auth/register` | 注册普通用户 `{username, email, password, nickname?}` → `201` |
| `POST` | `/api/v1/auth/login` | 登录 `{username, password}` → `{token, token_type, expires_at, user}`；**开启 MFA 后返回 `{mfa_required:true, mfa_token}`** |
| `POST` | `/api/v1/auth/logout` | 登出，撤销当前 token（需登录） |
| `GET`  | `/api/v1/auth/me` | 查看当前登录用户信息（需登录） |
| `PUT`  | `/api/v1/auth/password` | 修改自己密码 `{old_password, new_password}`（需登录） |
| `DELETE` | `/api/v1/auth/me` | **注销自己的账号** `{password}`（需登录；最后一个管理员不可注销） |

```bash
# 登录
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}'
# → {"token":"eyJ...","token_type":"Bearer","expires_at":"...","user":{...}}

# 查看自己
curl http://localhost:8080/api/v1/auth/me \
  -H "Authorization: Bearer $TOKEN"
```

登出后，同一 token 再访问会返回 `401 {"error":"token revoked, please login again"}`。

### 两步验证（MFA / TOTP）

基于 **TOTP（RFC 6238）**，兼容 Google Authenticator、Microsoft Authenticator、1Password、微信小程序类认证器等任意标准认证器 App。

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| `POST` | `/api/v1/auth/mfa/setup` | 生成密钥与二维码（需登录，未开启时） |
| `POST` | `/api/v1/auth/mfa/enable` | `{secret, code}` 校验并开启（需登录） |
| `POST` | `/api/v1/auth/mfa/verify` | 登录第二步：`{code}` + mfa_token → 正式 token |
| `DELETE` | `/api/v1/auth/mfa` | `{password, code}` 关闭两步验证（需登录） |
| `POST` | `/api/v1/auth/mfa/reset/request` | 邮箱重置：`{email, password}` → 向邮箱发送验证码（**公开**，无需登录） |
| `POST` | `/api/v1/auth/mfa/reset/confirm` | 邮箱重置：`{email, code}` 校验验证码并关闭两步验证（**公开**） |
| `DELETE` | `/api/v1/users/:id/mfa` | 管理员重置某用户的两步验证 |

**开启流程**

```bash
# 1) 生成密钥（用正式 token 调用）
curl -X POST http://localhost:8080/api/v1/auth/mfa/setup \
  -H "Authorization: Bearer $TOKEN"
# → {"secret":"JBSWY...","otpauth_url":"otpauth://totp/...","qr_code":"data:image/png;base64,..."}
#    qr_code 可直接放进 <img src>；认证器 App 扫码或手输 secret

# 2) 用 App 当前 6 位验证码确认开启
curl -X POST http://localhost:8080/api/v1/auth/mfa/enable \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"secret":"JBSWY...","code":"123456"}'
# → {"message":"两步验证已开启"}
```

**开启后登录变成两步**

```bash
# 第一步：账号密码 → 拿到 5 分钟有效的一次性 mfa_token（不是正式 token）
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' -d '{"username":"alice","password":"secret123"}'
# → {"mfa_required":true,"mfa_token":"eyJ...","expires_at":"..."}

# 第二步：认证器 App 验证码 → 正式 token
curl -X POST http://localhost:8080/api/v1/auth/mfa/verify \
  -H "Authorization: Bearer $MFA_TOKEN" -H 'Content-Type: application/json' \
  -d '{"code":"123456"}'
# → {"token":"eyJ...","token_type":"Bearer","user":{...,"mfa_enabled":true}}
```

**忘记验证器 / 换手机：邮箱重置**

用户账号必须已绑定邮箱（注册时填写，或通过 `PUT /users/:id` 修改）。重置流程不需要登录（用户已被锁在门外）：

```bash
# 1) 提交邮箱 + 密码，向邮箱发送 6 位验证码
curl -X POST http://localhost:8080/api/v1/auth/mfa/reset/request \
  -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"secret123"}'
# → {"message":"如果该邮箱存在且已开启两步验证，验证码已发送到邮箱"}

# 2) 用邮箱里的验证码确认，两步验证被关闭
curl -X POST http://localhost:8080/api/v1/auth/mfa/reset/confirm \
  -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","code":"123456"}'
# → {"message":"两步验证已重置，请使用密码登录后重新绑定认证器"}
```

之后用密码重新登录，再走一遍「开启流程」绑定新认证器即可。

**安全要点**

- `mfa_token` 有效期 **5 分钟且一次性**，用后立即失效；它**不能**访问任何业务接口（会被 `401 MFA verification required` 拒绝）。
- 邮箱验证码 **10 分钟有效、一次性**，最多错 5 次自动作废；`reset/request` 对不存在的邮箱/错误密码/未开启 MFA 一律返回相同提示，**不泄露账号是否存在**。
- 重置**必须同时**提供邮箱验证码（证明邮箱所有权）和账号密码；重置只关闭两步验证，不会签发任何 token。
- 用户换手机 / 丢失设备时，也可由**管理员**调用 `DELETE /users/:id/mfa` 重置。
- 邮件默认走 `console` 模式（验证码打印到服务端日志，仅开发用）；生产环境设置 `EMAIL_MODE=smtp` + `SMTP_*` 配置真实发信。
- 认证器 App 名称由 `MFA_ISSUER` 配置（默认 `wechatapp-web`）。
- 关闭两步验证需同时提供**密码**和**当前验证码**。

> 本地调试没有手机时，可用内置小工具生成验证码：
> `go run ./scripts/totp <base32-secret>`

### 人员管理接口（管理员）

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| `GET`    | `/api/v1/users` | 用户列表 `{total, items[]}` |
| `POST`   | `/api/v1/users` | 创建用户 `{username, password, nickname?, role?}`（role 默认 `user`） |
| `GET`    | `/api/v1/users/:id` | 查看指定用户（admin 或本人） |
| `PUT`    | `/api/v1/users/:id` | 修改昵称（本人/admin）、角色（仅 admin） |
| `DELETE` | `/api/v1/users/:id` | 删除用户 |

**权限规则**：

| 操作 | admin | user（本人） | user（他人） |
| ---- | ----- | ----------- | ------------ |
| 查看列表 / 创建 / 删除 | ✅ | ❌ `403` | ❌ `403` |
| 查看 / 修改昵称 | ✅ | ✅ | ❌ `403` |
| 修改角色 | ✅ | ❌ `403` | ❌ `403` |
| 注销自己账号（`DELETE /auth/me`） | ✅* | ✅ | — |
| 重置他人两步验证（`DELETE /users/:id/mfa`） | ✅ | ❌ `403` | ❌ `403` |

> `*` 最后一个管理员不可注销（防止系统失去管理员）。

> **存储后端**：默认内存存储；设置 `USERS_FILE=data/users.json` 可跨重启持久化（含密码哈希）；设置 `DB_HOST` 后改用 **MySQL**（推荐，启动时自动建库建表）。

## 接口

### `POST /api/v1/replace-background`

`multipart/form-data` 表单上传：

| 字段         | 类型   | 必填 | 说明                                            |
| ------------ | ------ | ---- | ----------------------------------------------- |
| `image`      | file   | 是   | 需要换背景的原图（jpg/png 等）                  |
| `background` | file   | 否   | 背景图片；提供时把抠出的前景合成到该图上        |
| `color`      | string | 否   | 背景色，hex 格式，如 `#ffffff` / `#fff`         |
| `size`       | string | 否   | 传给 remove.bg 的尺寸参数，默认 `auto`          |
| `format`     | string | 否   | 输出格式，`png`（默认）或 `jpeg`                |

> `background` 与 `color` 都提供时以 `background` 为准；都不提供时返回纯透明背景的抠图结果。

**成功**：`200`，响应体为处理后的图片（`Content-Type: image/png` 或 `image/jpeg`）。

**失败**：

| 状态码 | 场景                                        |
| ------ | ------------------------------------------- |
| `400`  | 缺少 `image` 字段、文件读取失败、颜色格式非法 |
| `502`  | remove.bg API 调用失败（密钥错误等）        |
| `500`  | 图片解码/合成失败                           |

示例：

```bash
# 换成纯色背景
curl -X POST http://localhost:8080/api/v1/replace-background \
  -F "image=@photo.jpg" -F "color=#00aaff" -o result.png

# 换成另一张背景图
curl -X POST http://localhost:8080/api/v1/replace-background \
  -F "image=@photo.jpg" -F "background=@beach.jpg" -o result.png

# 仅去背景（返回透明 PNG）
curl -X POST http://localhost:8080/api/v1/replace-background \
  -F "image=@photo.jpg" -o cutout.png
```

### `GET /healthz`

健康检查，返回 `{"status":"ok"}`。

## 彩票（大乐透）接口

支持 **大乐透**（Super Lotto）的中奖验证，并可直接从体彩官方接口自动获取开奖号码。

大乐透规则：前区 5 个号码（1-35）+ 后区 2 个号码（1-12）。奖级对照（依据中国体育彩票现行规则）：

| 奖级 | 中奖条件（前区+后区） | 奖池 8 亿以下 | 奖池 8 亿以上 |
| ---- | --------------------- | ------------- | ------------- |
| 一等奖 | 5+2 | 浮动（基本最高1000万） | 浮动（追加最高1800万） |
| 二等奖 | 5+1 | 浮动（追加多80%） | 浮动（追加多80%） |
| 三等奖 | 5+0、4+2 | 5,000元 | 6,666元 |
| 四等奖 | 4+1 | 300元 | 380元 |
| 五等奖 | 3+2、4+0 | 150元 | 200元 |
| 六等奖 | 3+1、2+2 | 15元 | 18元 |
| 七等奖 | 3+0、1+2、2+1、0+2 | 5元 | 7元 |

> **重要：真实票面二维码不含号码。** 体彩实体票的二维码内容是一个校验链接（如 `https://s.sporttery.cn/HD/wyWLPG`），号码是**票面打印文字**，且一张单式票通常有**多注**。因此：
> - 要验证实体票，请使用 **`POST /lottery/verify-ticket`** 提交票面号码（客户端录入/确认后调用，无需二维码）；
> - `POST /lottery/scan` / `verify` 仅适用于二维码内容**直接编码号码**的场景；若扫到链接会返回 `422` 并提示改用 `verify-ticket`。

### 推荐调用流程（客户端录入 / 确认）

```
①客户端录入号码 ──► POST /lottery/parse-ticket ──► 规范化号码 + 期号
                                                      │
                        ②用户在确认页核对 ◄───────────┘
                                                      │
                        ③POST /lottery/verify-ticket ──┴──► 中奖结果
```

### `POST /api/v1/lottery/parse-ticket` — 解析并规范化号码（录入/确认）

`application/json`。把客户端录入（或端上 OCR 识别）的号码做校验和规范化，返回统一格式供确认页展示。两种输入二选一：

| 字段   | 类型            | 说明                                        |
| ------ | --------------- | ------------------------------------------- |
| `text` | string          | 整票文本，**一行一注**（推荐，可直接粘贴票面文字） |
| `bets` | array           | 结构化号码，元素可为对象或字符串           |
| `issue`| string          | 期号（可选；`text` 中含「…期」时会自动识别） |

`text` 解析规则：自动跳过空行、`#` 注释行、票头/日期/金额/序列号等元信息行；含「期」的行识别为期号；号码不足 7 个的行会报错并**指明行号**。

```bash
# 直接粘贴整张票的文字
curl -X POST http://localhost:8080/api/v1/lottery/parse-ticket \
  -H 'Content-Type: application/json' \
  -d '{"text":"第 26102期\n① 04 10 12 16 20 + 10 12\n② 02 20 28 33 35 + 01 09"}'
```

**成功** `200`：

```json
{
  "issue": "26102",
  "total_bets": 2,
  "bets": [
    { "front": [4,10,12,16,20], "back": [10,12] },
    { "front": [2,20,28,33,35], "back": [1,9] }
  ]
}
```

**失败** `400`：号码非法或未解析出号码，`error` 会指明出错的行/注，例如
`第 1 行号码不足（04 10 12 16 20）：需要 7 个号码（前区5+后区2），实际 5 个`。

### `POST /api/v1/lottery/verify-ticket` — 整票验证（推荐）

`application/json`，支持一注或多注；**开奖号码可省略**，只传 `issue` 即自动从体彩官方接口拉取。

`bets` 支持三种写法，混用也可以：

```json
{
  "issue": "26102",
  "bets": [
    { "front": [4,10,12,16,20], "back": [10,12] },
    { "front": "02,20,28,33,35", "back": "01,09" },
    "11 13 20 25 34 + 03 12"
  ]
}
```

也可以用 `text`（一行一注）代替 `bets`：

```json
{
  "issue": "26102",
  "text": "04 10 12 16 20 + 10 12\n02 20 28 33 35 + 01 09"
}
```

也可以显式传入开奖号码（此时 `issue` 可省略）：

```json
{ "winning_front": "01,03,07,27,28", "winning_back": "06,07", "bets": [ ... ] }
```

**成功** `200`：

```json
{
  "issue": "26102",
  "winning": {
    "issue": "26102", "draw_time": "2026-09-07",
    "front": [1,3,7,27,28], "back": [6,7],
    "pool_balance": 725321799.11, "pool_above_8yi": false
  },
  "total_bets": 5, "winning_bets": 0, "total_won": false,
  "bets": [
    { "index": 1, "front": [4,10,12,16,20], "back": [10,12],
      "matched_front": 0, "matched_back": 0, "won": false },
    { "index": 4, "front": [9,11,15,28,29], "back": [7,12],
      "matched_front": 1, "matched_back": 1, "won": false }
  ]
}
```

中奖时该注会带 `prize`（奖级信息）和按奖池档位算出的 `amount`，例如：

```json
{ "index": 1, "matched_front": 2, "matched_back": 1, "won": true,
  "prize": { "tier": 7, "tier_name": "七等奖", "condition": "2+1" },
  "amount": "5元" }
```

**失败**：`400`（号码非法/缺少开奖号与期号）、`502`（拉取官方开奖号失败）。

### `GET /api/v1/lottery/winning/:issue` — 查询官方开奖号码

从体彩官方接口获取指定期号的开奖结果（进程内缓存）。

```bash
curl http://localhost:8080/api/v1/lottery/winning/26102
# {"issue":"26102","draw_time":"2026-09-07","front":[1,3,7,27,28],"back":[6,7],
#  "pool_balance":725321799.11,"pool_above_8yi":false}
```

**失败**：`404`（期号不存在）、`502`（上游接口异常）。

### `POST /api/v1/lottery/scan` — 识别二维码中的号码

> 仅适用于二维码内容**直接编码号码**的场景（非实体票）。

`multipart/form-data`：

| 字段    | 类型 | 必填 | 说明               |
| ------- | ---- | ---- | ------------------ |
| `qrcode` | file | 是   | 含大乐透号码的二维码图片 |

二维码内容需包含 7 个号码（前区 5 个 + 后区 2 个），分隔符不限（逗号、空格、加号、中文均可），例如 `05,12,18,23,35,01,08` 或 `05 12 18 23 35 + 01 08`。

**成功** `200`：

```json
{
  "raw": "05,12,18,23,35,01,08",
  "ticket": { "front": [5,12,18,23,35], "back": [1,8] }
}
```

扫描到链接（实体票）时返回 `422`：

```json
{ "error": "二维码内容是链接而非号码（https://s.sporttery.cn/HD/wyWLPG）。真实票面二维码只含校验链接，请改用 /lottery/verify-ticket 提交票面号码" }
```

### `POST /api/v1/lottery/verify` — 单注二维码验证

> 仅适用于二维码内容**直接编码号码**的场景（非实体票）。实体票请用 `verify-ticket`。

`multipart/form-data`：

| 字段            | 类型   | 必填 | 说明                       |
| --------------- | ------ | ---- | -------------------------- |
| `qrcode`        | file   | 是   | 含用户打印号码的二维码图片 |
| `winning_front` | string | 是   | 开奖前区号码，如 `05,12,18,23,35` |
| `winning_back`  | string | 是   | 开奖后区号码，如 `01,08`   |

**成功** `200`（中奖）：

```json
{
  "matched_front": 5,
  "matched_back": 2,
  "won": true,
  "prize": {
    "tier": 1,
    "tier_name": "一等奖",
    "condition": "5+2",
    "amount_below_8yi": "浮动（基本投注最高1000万）",
    "amount_above_8yi": "浮动（追加投注最高1800万）"
  }
}
```

未中奖时 `won: false`，无 `prize` 字段。

示例：

```bash
# 识别二维码号码
curl -X POST http://localhost:8080/api/v1/lottery/scan \
  -F "qrcode=@ticket_qr.png"

# 验证中奖
curl -X POST http://localhost:8080/api/v1/lottery/verify \
  -F "qrcode=@ticket_qr.png" \
  -F "winning_front=05,12,18,23,35" \
  -F "winning_back=01,08"
```

## 项目结构

```
wechatapp-web/
├── cmd/
│   └── server/
│       └── main.go                # 程序入口（含 Swagger 全局信息注解）
├── internal/
│   ├── config/
│   │   └── config.go              # 环境变量配置
│   ├── auth/
│   │   ├── jwt.go                 # JWT 签发/解析/黑名单（登出撤销）+ MFA challenge token
│   │   ├── middleware.go          # Gin 鉴权中间件（RequireAuth / RequireRole / RequireMFAChallenge）
│   │   ├── totp.go                # TOTP（RFC 6238）密钥/验证/二维码
│   │   ├── emailotp.go            # 邮箱验证码（MFA 重置用）：生成/校验/过期/防爆破
│   │   ├── password.go            # bcrypt 密码哈希 + 用户名校验
│   │   ├── jwt_test.go
│   │   └── totp_test.go
│   ├── user/
│   │   ├── user.go                # 用户模型 + 内存存储（Store 接口）
│   │   ├── mysql.go               # MySQL 存储（建库建表、增删改查）
│   │   ├── persist.go             # JSON 文件持久化（可选）
│   │   ├── id.go                  # 用户 ID 生成
│   │   ├── user_test.go
│   │   └── mysql_test.go          # MySQL 集成测试（无库时自动跳过）
│   ├── handler/
│   │   ├── background.go          # 换背景 Gin handler（含 Swagger 接口注解）
│   │   ├── lottery.go             # 大乐透 scan / verify Gin handler
│   │   ├── lottery_ticket.go      # 号码解析/整票验证/开奖号码查询 handler
│   │   ├── auth.go                # 注册/登录/登出/me/改密 handler
│   │   ├── mfa.go                 # 两步验证 handler（setup/enable/verify/disable/邮箱重置/管理员重置）
│   │   ├── user.go                # 人员管理 handler（管理员）
│   │   ├── background_test.go
│   │   ├── lottery_test.go
│   │   └── auth_test.go
│   ├── mail/
│   │   ├── sender.go              # 邮件发送：console（开发）/ SMTP 两种实现
│   │   └── sender_test.go
│   ├── lottery/
│   │   ├── lottery.go             # 大乐透号码解析、匹配、奖级判定
│   │   ├── ticket.go              # 多注整票模型与整票验证
│   │   ├── winning.go             # 体彩官方开奖号码获取（含缓存）
│   │   ├── qr.go                  # 二维码解码（gozxing）
│   │   └── *_test.go
│   ├── router/
│   │   └── router.go              # 路由注册 + 中间件装配 + 管理员播种
│   └── service/
│       ├── removebg.go            # remove.bg API 客户端（对应 Python 参考实现）
│       ├── composite.go           # 背景合成：纯色 / 背景图 / 仅抠图
│       └── composite_test.go
├── docs/                          # swag 生成的接口文档（docs.go / json / yaml）
├── scripts/
│   ├── mock_removebg.py           # 本地 mock remove.bg 服务（手动冒烟测试用）
│   ├── genqr/                     # 生成测试二维码的辅助程序
│   └── totp/                      # 打印当前 TOTP 验证码（本地调试 MFA 用）
├── testdata/                      # 测试图片（含测试二维码）
├── Makefile                       # 构建 / 测试 / 文档 / Docker 等常用任务
├── Dockerfile                     # 多阶段构建
├── .env.example                   # 环境变量示例
└── .dockerignore / .gitignore
```

## Makefile

```bash
make build         # 编译二进制到 ./bin/wechatapp-server
make run           # 本地运行（需 REMOVE_BG_API_KEY）
make test          # 运行单元测试
make vet           # go vet 静态检查
make fmt           # gofmt 格式化
make tidy          # 同步 go.mod / go.sum
make swagger       # 重新生成 docs/（自动安装 swag CLI）
make docker-build  # 构建 Docker 镜像
make docker-run    # 运行容器（加载 .env，映射 8080 端口）
make docker-stop   # 停止容器
make clean         # 清理构建产物与本地工具
make help          # 查看全部目标
```

可覆盖的变量：`GOMODCACHE`、`GOCACHE`、`GOPROXY`、`GOSUMDB`，例如：

```bash
make build GOPROXY=https://proxy.golang.org,direct GOSUMDB=on
```

## Docker

```bash
# 构建镜像
make docker-build
# 或
docker build -t wechatapp-web:latest .

# 运行（加载 .env 中的 REMOVE_BG_API_KEY，映射宿主机 8080）
make docker-run

# 手动运行（务必设置 JWT_SECRET 和初始管理员密码）
docker run --rm -d --name wechatapp-web -p 8080:8080 \
  -e REMOVE_BG_API_KEY=your-api-key-here \
  -e JWT_SECRET=change-me-to-a-long-random-string \
  -e ADMIN_PASSWORD=admin123 \
  wechatapp-web:latest

# 查看 Swagger 文档
# http://localhost:8080/swagger/index.html
```

镜像特点：多阶段构建（`golang:1.22-alpine` 编译 → `alpine:3.20` 运行）、静态二进制、非 root 用户、内置健康检查。

## Swagger 文档

接口注解写在 handler 与 main 中，使用 [swaggo/swag](https://github.com/swaggo/swag) 生成：

```bash
make swagger     # 重新生成 docs/
```

- 文档页面：`/swagger/index.html`
- 原始定义：`docs/swagger.json`、`docs/swagger.yaml`

> 修改接口注解后记得 `make swagger` 重新生成并提交 `docs/`。

## 环境变量

见 `.env.example`：

| 变量                 | 默认值                              | 说明                           |
| -------------------- | ----------------------------------- | ------------------------------ |
| `REMOVE_BG_API_KEY`  | —（必填）                           | remove.bg API 密钥             |
| `REMOVE_BG_ENDPOINT` | `https://api.remove.bg/v1.0/removebg` | remove.bg 接口地址（可覆盖，便于测试） |
| `LOTTERY_WINNING_ENDPOINT` | `https://webapi.sporttery.cn/gateway/lottery/getHistoryPageListV1.qry` | 体彩官方开奖号码接口（可覆盖，便于测试） |
| `JWT_SECRET`         | —（空则启动时随机生成）             | JWT 签名密钥（**生产必填**）   |
| `JWT_TTL`            | `24h`                               | token 有效期（Go duration）    |
| `JWT_ISSUER`         | `wechatapp-web`                     | token 的 issuer 声明           |
| `MFA_ISSUER`         | `wechatapp-web`                     | 认证器 App 中显示的名称        |
| `EMAIL_MODE`         | `console`                           | 邮件发送方式：`console`（日志）/ `smtp` |
| `SMTP_HOST`          | —                                   | SMTP 服务器地址                |
| `SMTP_PORT`          | —                                   | SMTP 端口（如 587）            |
| `SMTP_USER`          | —                                   | SMTP 用户名（发件账号）        |
| `SMTP_PASSWORD`      | —                                   | SMTP 密码 / 授权码             |
| `SMTP_FROM`          | —                                   | 发件人地址（缺省用 SMTP_USER） |
| `DB_HOST`            | —（内存存储）                       | 设为非空启用 MySQL 用户存储     |
| `DB_PORT`            | `3306`                              | MySQL 端口                     |
| `DB_USER`            | `root`                              | MySQL 用户名                   |
| `DB_PASSWORD`        | —                                   | MySQL 密码                     |
| `DB_NAME`            | —                                   | MySQL 库名（自动创建）         |
| `DB_CHARSET`         | `utf8mb4`                           | MySQL 字符集                   |
| `USERS_FILE`         | —（内存存储）                       | 用户持久化 JSON 文件路径（仅未配置 DB_HOST 时使用） |
| `ADMIN_USERNAME`     | `admin`                             | 初始管理员用户名               |
| `ADMIN_PASSWORD`     | `admin123`                          | 初始管理员密码（**部署前修改**） |
| `LISTEN_ADDR`        | `:8080`                             | HTTP 监听地址                  |

## 测试

```bash
make test
```

- `internal/service/composite_test.go`：背景合成单元测试（纯色、背景图、仅抠图、颜色解析）。
- `internal/handler/background_test.go`：用 `httptest` 模拟 remove.bg 服务的端到端 handler 测试。
- `internal/lottery/lottery_test.go`：大乐透号码解析与全部奖级（一等奖~七等奖）判定测试。
- `internal/lottery/qr_test.go`：二维码解码（`scripts/genqr` 生成测试二维码）与扫码验证集成测试。
- `internal/lottery/ticket_test.go`：多注整票验证、整票文本解析、真实票面复现、官方开奖接口解析与缓存测试。
- `internal/handler/lottery_test.go`：`parse-ticket` / `verify-ticket` / `winning` 接口的端到端 handler 测试（mock 官方接口）。
- `internal/auth/jwt_test.go`：JWT 签发/解析/过期/篡改/登出撤销测试。
- `internal/auth/totp_test.go`：TOTP 校验（含时钟漂移窗口）、二维码生成测试。
- `internal/auth/emailotp_test.go`：邮箱验证码生成/校验/过期/防爆破/重发冷却测试。
- `internal/user/user_test.go`：用户存储增删改查与 JSON 文件持久化测试。
- `internal/user/mysql_test.go`：MySQL 存储集成测试（设置 `TEST_DB_HOST` 等环境变量时运行，否则自动跳过；含遗留 NULL 列回归测试）。
- `internal/handler/auth_test.go`：注册/登录/登出/改密/人员管理/MFA 全流程的端到端 handler 测试（含角色权限校验）。

手动冒烟测试（可选，无需真实 API Key）：

```bash
python3 scripts/mock_removebg.py          # 终端 1：起 mock remove.bg
REMOVE_BG_API_KEY=test REMOVE_BG_ENDPOINT=http://127.0.0.1:18098/v1.0/removebg \
  ./bin/wechatapp-server                  # 终端 2：起服务并指向 mock
```

## 说明

- 依赖 remove.bg API，需要注册获取 [API Key](https://www.remove.bg/api)。
- 背景合成使用 Go 标准库 `image/draw` 做 alpha 合成，`golang.org/x/image/draw` 做高质量缩放。
- 本机 Go 1.22 环境使用 gin v1.10.0（最新 gin 需要更高 Go 版本）。
