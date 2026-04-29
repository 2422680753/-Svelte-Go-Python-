# 短视频内容风控与审核平台 - 部署指南

## 系统架构

```
┌─────────────────────────────────────────────────────────────────┐
│                         前端 (Svelte + Vite)                      │
│  ┌─────────────┬─────────────┬─────────────┬─────────────┐      │
│  │  登录/注册   │  审核工作台  │  任务列表   │  统计报表   │      │
│  ├─────────────┼─────────────┼─────────────┼─────────────┤      │
│  │  视频预览    │  违规标记   │  批量操作   │  申诉处理   │      │
│  └─────────────┴─────────────┴─────────────┴─────────────┘      │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                      后端 API 服务 (Go + Gin)                     │
│  ┌─────────────┬─────────────┬─────────────┬─────────────┐      │
│  │  认证模块    │  任务分发   │  审核状态机 │  批量操作   │      │
│  ├─────────────┼─────────────┼─────────────┼─────────────┤      │
│  │  申诉服务    │  统计服务   │  日志服务   │  SLA监控    │      │
│  └─────────────┴─────────────┴─────────────┴─────────────┘      │
│                                                                     │
│  ┌─────────────────────────────┐  ┌─────────────────────────┐   │
│  │      PostgreSQL (数据存储)   │  │    Redis (缓存/队列)     │   │
│  │  - 用户管理                  │  │  - 任务队列              │   │
│  │  - 视频/任务数据             │  │  - 会话缓存              │   │
│  │  - 审核记录/日志             │  │  - 实时通知              │   │
│  │  - 统计报表                  │  │  - 分布式锁              │   │
│  └─────────────────────────────┘  └─────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    机审服务 (Python + Flask)                      │
│  ┌─────────────┬─────────────┬─────────────┬─────────────┐      │
│  │  视频抽帧    │  图像识别   │  文本检测   │  音频分析   │      │
│  ├─────────────┼─────────────┼─────────────┼─────────────┤      │
│  │  - OpenCV   │  - 皮肤检测 │  - 敏感词   │  - 转文本   │      │
│  │  - FFmpeg   │  - 颜色分析 │  - 正则匹配 │  - 关键词   │      │
│  │  - 帧同步    │  - 人脸检测 │  - 分类器   │  - 分类     │      │
│  └─────────────┴─────────────┴─────────────┴─────────────┘      │
└─────────────────────────────────────────────────────────────────┘
```

## 技术栈

| 层级 | 技术 | 版本 |
|------|------|------|
| 前端 | Svelte + Vite | 4.x / 5.x |
| 路由 | svelte-routing | 2.x |
| 图表 | Chart.js + svelte-chartjs | 4.x / 3.x |
| HTTP | Axios | 1.x |
| 后端 | Go + Gin | 1.21+ / 1.9.x |
| ORM | GORM | 1.25.x |
| 数据库 | PostgreSQL | 12+ |
| 缓存/队列 | Redis | 6.x+ |
| 认证 | JWT | 5.x |
| 机审 | Python + Flask | 3.9+ / 2.x |
| 视频处理 | OpenCV + FFmpeg | 4.x+ |
| 文本处理 | jieba + 正则 | 0.42.x |

## 核心功能模块

### 1. 审核状态机 (State Machine)

**状态流转：**
```
pending → auto_moderating → { auto_approved / auto_rejected / need_review }
                                    ↓               ↓               ↓
                                published         banned        assigned
                                                         ↓
                                                    in_review → { human_approved / human_rejected }
                                                                    ↓               ↓
                                                               published         banned
                                                                    ↓
                                                              appealed → { appeal_approved / appeal_rejected }
                                                                      ↓               ↓
                                                               need_review       banned
```

**关键状态说明：**
- `pending`: 初始状态，等待进入机审队列
- `auto_moderating`: 机审服务正在处理中
- `auto_approved`: 机审自动通过（分数 < 0.1）
- `auto_rejected`: 机审自动拒绝（分数 > 0.9）
- `need_review`: 机审结果不确定，需要人工审核（0.1 < 分数 < 0.9）
- `assigned`: 任务已分配给审核员
- `in_review`: 审核员正在审核
- `human_approved`: 人工审核通过
- `human_rejected`: 人工审核拒绝
- `appealed`: 用户提交申诉
- `appeal_approved`: 申诉通过，重新进入审核
- `appeal_rejected`: 申诉驳回，维持原决定
- `published`: 视频已发布
- `banned`: 视频已封禁

### 2. 任务分发系统

**优先级队列：**
- `high`: 高优先级（SLA < 6小时、重要用户、敏感内容）
- `normal`: 普通优先级
- `low`: 低优先级

**分配策略：**
1. 自动分配：根据审核员工作量自动分配任务
2. 手动分配：管理员/高级审核员手动分配
3. 抢单模式：审核员自行领取任务（可选）

**负载均衡：**
- 统计每个审核员的待处理任务数
- 优先分配给任务数最少的审核员
- 考虑审核员的专长领域（可选）

### 3. 视频帧同步

**抽帧策略：**
- 默认间隔：5秒一帧
- 可配置：根据视频长度动态调整
- 关键帧：场景切换自动多抽帧

**分析流程：**
1. 下载视频到本地临时目录
2. 使用 FFmpeg 按间隔提取帧
3. 使用 OpenCV 进行图像分析：
   - 皮肤检测（判断是否色情）
   - 红色区域检测（判断是否暴力/血腥）
   - 亮度/对比度分析
   - 人脸检测
4. 将分析结果回写数据库
5. 缓存标记帧用于前端展示

### 4. 机审结果回写

**评分机制：**
```
总体风险分数 = max(视频帧分析分数, 文本分析分数, 音频分析分数)
```

**自动决策阈值：**
- 自动通过：分数 < 0.1（10%）
- 自动拒绝：分数 > 0.9（90%）
- 需要人工：0.1 ≤ 分数 ≤ 0.9

**违规类别：**
| 类别 | 说明 |
|------|------|
| violence | 暴力血腥 |
| nudity | 色情低俗 |
| hate_speech | 仇恨言论 |
| misinformation | 虚假信息 |
| sensitive | 政治/宗教敏感 |
| gambling | 赌博相关 |
| drugs | 毒品相关 |
| politics | 政治敏感 |
| fraud | 诈骗虚假 |

### 5. 人工复审流程

**审核工作台功能：**
1. 视频预览：支持播放、暂停、拖拽
2. 帧列表：显示所有提取的帧，标记帧高亮
3. 机审结果：展示机审分数、推荐操作、违规细节
4. 违规标签：多标签选择（拒绝时必填）
5. 审核备注：可选输入
6. 审核日志：完整的操作记录

**批量操作：**
- 批量通过
- 批量拒绝（支持统一违规标签和备注）
- 批量分配
- 批量发布
- 批量封禁

### 6. 申诉处理

**申诉流程：**
1. 用户对审核结果有异议，提交申诉
2. 申诉进入待处理队列
3. 高级审核员/管理员处理申诉
4. 处理结果：
   - 申诉通过：重新进入人工审核
   - 申诉驳回：维持原决定

**申诉处理能力：**
- 查看原审核记录
- 重新审核视频内容
- 对比机审和人审结果
- 记录申诉处理结果

### 7. 审核 SLA

**SLA 配置：**
- 默认 SLA：24小时
- 高优先级任务：6小时
- 可根据业务需求调整

**SLA 监控：**
- 实时计算剩余时间
- 临近 SLA 截止自动标记（< 6小时）
- 超时自动升级优先级
- 统计报表展示 SLA 达标率

### 8. 统计报表

**仪表盘：**
- 待审核任务数
- 进行中任务数
- 今日通过数
- 今日拒绝数
- 待处理申诉数
- SLA 达标率

**图表展示：**
- 7天审核趋势图（折线图）
- 每日通过/拒绝对比（柱状图）
- 审核结果分布（饼图）
- 违规类别分布（极坐标图）

**审核员绩效：**
- 总审核数
- 通过/拒绝分布
- 平均审核时长
- 申诉维持数
- 申诉改判数
- 准确率评分

**违规标签统计：**
- 各类违规标签的出现频次
- 趋势分析

## 部署步骤

### 1. 环境准备

**必需软件：**
- Go 1.21+
- Python 3.9+
- Node.js 18+
- PostgreSQL 12+
- Redis 6+
- FFmpeg (视频处理)

### 2. 数据库初始化

**PostgreSQL：**
```sql
-- 创建数据库
CREATE DATABASE content_moderation;

-- 创建用户（可选）
CREATE USER moderation_user WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE content_moderation TO moderation_user;
```

**Redis：**
```bash
# 确保 Redis 服务运行
redis-cli ping
# 返回 PONG 表示正常
```

### 3. 后端服务部署

**步骤：**
```bash
# 进入后端目录
cd backend

# 配置环境变量
cp .env.example .env
# 编辑 .env 文件，配置数据库连接等参数

# 下载依赖
go mod download

# 编译运行
go run main.go

# 或编译为二进制文件
go build -o moderation-server main.go
./moderation-server
```

**环境变量配置 (.env)：**
```env
# Server
SERVER_PORT=8080
SERVER_MODE=debug

# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=content_moderation
DB_SSLMODE=disable

# Redis
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0

# JWT
JWT_SECRET=your-secret-key-change-in-production
JWT_EXPIRE_HOURS=24

# Moderation
AUTO_REJECT_THRESHOLD=0.9
AUTO_APPROVE_THRESHOLD=0.1
NEED_REVIEW_THRESHOLD=0.5
VIDEO_FRAME_INTERVAL=5
SLA_HOURS=24
```

### 4. Python 机审服务部署

**步骤：**
```bash
# 进入机审服务目录
cd moderation-service

# 创建虚拟环境（推荐）
python -m venv venv
source venv/bin/activate  # Linux/Mac
# 或
venv\Scripts\activate  # Windows

# 安装依赖
pip install -r requirements.txt

# 配置环境变量
cp .env.example .env
# 编辑 .env 文件

# 运行服务
python app.py

# 或使用 Gunicorn 生产环境部署
gunicorn -w 4 -b 0.0.0.0:5000 app:app
```

**环境变量配置 (.env)：**
```env
PORT=5000
DEBUG=false
FRAME_INTERVAL=5
AUTO_REJECT_THRESHOLD=0.9
AUTO_APPROVE_THRESHOLD=0.1
NEED_REVIEW_THRESHOLD=0.5
```

### 5. 前端部署

**开发环境：**
```bash
# 进入前端目录
cd frontend

# 安装依赖
npm install

# 开发模式运行
npm run dev

# 访问 http://localhost:3000
```

**生产环境构建：**
```bash
# 构建生产版本
npm run build

# 构建产物在 dist 目录
# 使用 Nginx 或其他 Web 服务器部署
```

### 6. 完整启动顺序

```bash
# 1. 启动 PostgreSQL
sudo systemctl start postgresql  # Linux
# 或 Windows 服务管理

# 2. 启动 Redis
sudo systemctl start redis  # Linux
# 或 Windows 服务管理

# 3. 启动后端 Go 服务
cd backend
go run main.go

# 4. 启动 Python 机审服务
cd moderation-service
source venv/bin/activate
python app.py

# 5. 启动前端开发服务
cd frontend
npm run dev
```

## API 接口说明

### 认证接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/auth/register | 用户注册 |
| POST | /api/v1/auth/login | 用户登录 |
| GET | /api/v1/auth/me | 获取当前用户信息 |

### 任务接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/videos | 创建视频和审核任务 |
| GET | /api/v1/tasks | 获取任务列表（支持筛选） |
| GET | /api/v1/tasks/:id | 获取任务详情 |
| POST | /api/v1/tasks/:id/start-review | 开始审核 |
| POST | /api/v1/tasks/:id/submit-review | 提交审核结果 |
| POST | /api/v1/tasks/:id/assign | 分配任务 |
| GET | /api/v1/tasks/my-pending | 获取我的待处理任务 |
| GET | /api/v1/tasks/my-history | 获取我的审核历史 |

### 批量操作接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/batch | 创建批量操作 |
| GET | /api/v1/batch/:id | 获取批量操作状态 |

**批量操作类型：**
- `batch_approve`: 批量通过
- `batch_reject`: 批量拒绝
- `batch_assign`: 批量分配
- `batch_publish`: 批量发布
- `batch_ban`: 批量封禁

### 申诉接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/appeals | 提交申诉 |
| GET | /api/v1/appeals/my | 获取我的申诉 |
| GET | /api/v1/appeals/pending | 获取待处理申诉（管理员） |
| GET | /api/v1/appeals/:id | 获取申诉详情 |
| POST | /api/v1/appeals/:id/assign | 分配申诉处理人 |
| POST | /api/v1/appeals/:id/resolve | 处理申诉 |

### 统计接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/stats/dashboard | 仪表盘统计 |
| GET | /api/v1/stats/trends | 趋势数据 |
| GET | /api/v1/stats/reviewers | 审核员绩效 |
| GET | /api/v1/stats/violations | 违规统计 |
| GET | /api/v1/stats/sla | SLA 性能 |

### 机审服务接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/moderate | 综合内容审核 |
| POST | /api/v1/analyze-text | 仅文本分析 |
| POST | /api/v1/analyze-frames | 分析视频帧 |
| POST | /api/v1/extract-frames | 从 URL 提取帧 |
| GET | /health | 健康检查 |

## 数据模型

### 核心表结构

**用户表 (users)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| username | VARCHAR(255) | 用户名（唯一） |
| email | VARCHAR(255) | 邮箱（唯一） |
| password_hash | VARCHAR(255) | 密码哈希 |
| role | VARCHAR(50) | 角色：admin/senior_reviewer/reviewer |
| department | VARCHAR(100) | 部门 |
| is_active | BOOLEAN | 是否启用 |
| last_login_at | TIMESTAMP | 最后登录时间 |

**视频表 (videos)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| title | VARCHAR(500) | 视频标题 |
| description | TEXT | 视频描述 |
| video_url | VARCHAR(1000) | 视频 URL |
| thumbnail_url | VARCHAR(1000) | 缩略图 URL |
| duration | INTEGER | 时长（秒） |
| file_size | BIGINT | 文件大小（字节） |
| uploader_id | UUID | 上传者 ID |
| status | VARCHAR(50) | 状态 |
| is_published | BOOLEAN | 是否已发布 |

**审核任务表 (moderation_tasks)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| video_id | UUID | 视频 ID |
| assigned_to | UUID | 分配给 |
| current_status | VARCHAR(50) | 当前状态 |
| previous_status | VARCHAR(50) | 之前状态 |
| priority | VARCHAR(20) | 优先级 |
| sla_deadline | TIMESTAMP | SLA 截止时间 |
| auto_moderation_at | TIMESTAMP | 机审完成时间 |
| human_review_at | TIMESTAMP | 人审完成时间 |
| completed_at | TIMESTAMP | 完成时间 |

**违规标签表 (violation_tags)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| name | VARCHAR(100) | 标签名称 |
| description | TEXT | 描述 |
| category | VARCHAR(50) | 类别 |
| severity | VARCHAR(20) | 严重程度 |
| is_active | BOOLEAN | 是否启用 |

**机审结果表 (auto_moderation_results)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| task_id | UUID | 任务 ID |
| overall_score | FLOAT | 总体分数 |
| violation_scores | JSONB | 各类别分数 |
| flagged_frames | JSONB | 标记帧 |
| text_analysis | JSONB | 文本分析结果 |
| audio_analysis | JSONB | 音频分析结果 |
| recommendation | VARCHAR(20) | 推荐操作 |
| processing_time | FLOAT | 处理时长（秒） |

**人审结果表 (human_moderation_results)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| task_id | UUID | 任务 ID |
| reviewer_id | UUID | 审核员 ID |
| decision | VARCHAR(20) | 决定 |
| violation_tags | JSONB | 违规标签 |
| comment | TEXT | 备注 |
| review_duration | FLOAT | 审核时长（秒） |
| is_appeal | BOOLEAN | 是否申诉审核 |
| appeal_id | UUID | 申诉 ID |

**视频帧表 (video_frames)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| video_id | UUID | 视频 ID |
| frame_index | INTEGER | 帧序号 |
| timestamp | FLOAT | 时间点（秒） |
| frame_url | VARCHAR(1000) | 帧图片 URL |
| analysis | JSONB | 分析结果 |
| is_flagged | BOOLEAN | 是否标记 |

**申诉表 (appeals)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| task_id | UUID | 任务 ID |
| video_id | UUID | 视频 ID |
| original_decision | VARCHAR(50) | 原决定 |
| reason | TEXT | 申诉原因 |
| submitted_by | UUID | 提交人 |
| status | VARCHAR(50) | 状态 |
| assigned_to | UUID | 分配给 |
| appeal_result | VARCHAR(20) | 申诉结果 |
| appeal_comment | TEXT | 申诉备注 |
| resolved_at | TIMESTAMP | 解决时间 |

**审核日志表 (moderation_logs)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| task_id | UUID | 任务 ID |
| video_id | UUID | 视频 ID |
| actor_type | VARCHAR(20) | 操作方类型 |
| actor_id | UUID | 操作人 ID |
| action | VARCHAR(50) | 操作类型 |
| from_status | VARCHAR(50) | 从状态 |
| to_status | VARCHAR(50) | 到状态 |
| details | JSONB | 详细信息 |
| comment | TEXT | 备注 |

**批量操作表 (batch_operations)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| operator_id | UUID | 操作人 ID |
| operation_type | VARCHAR(50) | 操作类型 |
| task_ids | JSONB | 任务 ID 列表 |
| total_count | INTEGER | 总数 |
| success_count | INTEGER | 成功数 |
| failed_count | INTEGER | 失败数 |
| status | VARCHAR(20) | 状态 |
| parameters | JSONB | 参数 |
| error_details | JSONB | 错误详情 |

**每日统计表 (daily_statistics)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| date | DATE | 日期（唯一） |
| total_videos | INTEGER | 总视频数 |
| auto_moderated | INTEGER | 机审数 |
| human_reviewed | INTEGER | 人审数 |
| auto_approved | INTEGER | 机审通过数 |
| auto_rejected | INTEGER | 机审拒绝数 |
| need_review | INTEGER | 需人审数 |
| human_approved | INTEGER | 人审通过数 |
| human_rejected | INTEGER | 人审拒绝数 |
| average_review_time | FLOAT | 平均审核时长 |
| sla_met | INTEGER | SLA 达标数 |
| sla_missed | INTEGER | SLA 未达标数 |
| appeals_received | INTEGER | 收到申诉数 |
| appeals_resolved | INTEGER | 处理申诉数 |

**审核员绩效表 (reviewer_performance)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| reviewer_id | UUID | 审核员 ID |
| date | DATE | 日期 |
| total_reviews | INTEGER | 总审核数 |
| approved_count | INTEGER | 通过数 |
| rejected_count | INTEGER | 拒绝数 |
| average_review_time | FLOAT | 平均审核时长 |
| appeals_uphold_count | INTEGER | 申诉维持数 |
| appeals_overturn_count | INTEGER | 申诉改判数 |
| accuracy_score | FLOAT | 准确率 |

## 扩展与定制

### 1. 接入真实 AI 模型

当前机审服务使用的是基于规则的检测（颜色分析、关键词匹配）。可以接入真实 AI 模型提升检测准确率：

**图像/视频识别：**
- 接入腾讯云内容安全、阿里云内容安全等云服务
- 使用开源模型：YOLO、ResNet、MobileNet 等进行物体检测
- 使用 NSFW 模型检测色情内容
- 使用人脸检测 + 人脸比对识别公众人物

**文本识别：**
- 使用 BERT、RoBERTa 等预训练模型进行文本分类
- 接入中文敏感词检测服务
- 使用情感分析模型

**音频识别：**
- 使用 Whisper 进行语音转文本
- 使用声纹识别
- 音频情感分析

### 2. 视频处理优化

**分布式抽帧：**
- 使用消息队列（Kafka/RabbitMQ）解耦抽帧任务
- 多 Worker 并行处理
- 支持任务重试和失败补偿

**智能抽帧：**
- 场景切换检测（使用帧差法）
- 关键帧优先分析
- 动态调整抽帧间隔

### 3. 实时通知系统

**WebSocket 实时推送：**
- 任务分配实时通知
- SLA 临近提醒
- 申诉处理通知
- 批量操作完成通知

**消息队列：**
- 使用 Redis Pub/Sub 进行轻量级通知
- 使用 Kafka 处理高吞吐量消息

### 4. 权限与安全

**RBAC 权限模型：**
```
超级管理员 (super_admin)
  ├── 管理员 (admin)
  │     ├── 高级审核员 (senior_reviewer)
  │     │     └── 审核员 (reviewer)
  │     └── 运营人员 (operator)
  └── 审计人员 (auditor)
```

**数据权限：**
- 审核员只能查看分配给自己的任务
- 高级审核员可以查看部门内所有任务
- 管理员可以查看所有任务

**操作审计：**
- 所有操作都有日志记录
- 支持操作追溯
- 定期审计报告

### 5. 高可用部署

**服务架构：**
```
                    ┌─────────────┐
                    │   Nginx     │
                    │  (负载均衡)  │
                    └──────┬──────┘
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
    ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
    │  Frontend   │ │  Frontend   │ │  Frontend   │
    │  (Svelte)   │ │  (Svelte)   │ │  (Svelte)   │
    └──────┬──────┘ └──────┬──────┘ └──────┬──────┘
           └────────────────┼────────────────┘
                            ▼
                    ┌─────────────┐
                    │   Nginx     │
                    │  (API网关)   │
                    └──────┬──────┘
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
    ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
    │  Backend    │ │  Backend    │ │  Backend    │
    │  (Go)       │ │  (Go)       │ │  (Go)       │
    └──────┬──────┘ └──────┬──────┘ └──────┬──────┘
           │                │                │
           └────────────────┼────────────────┘
                            ▼
              ┌─────────────────────────────┐
              │     Redis Cluster           │
              │    (Session + 队列 + 缓存)   │
              └─────────────────────────────┘
                            ▼
              ┌─────────────────────────────┐
              │   PostgreSQL Primary        │
              │         ┌─────────┐         │
              │         │ Replica │         │
              │         │ (从库)   │         │
              │         └─────────┘         │
              └─────────────────────────────┘
                            ▼
           ┌────────────────┼────────────────┐
           ▼                                 ▼
    ┌─────────────┐                   ┌─────────────┐
    │  Python     │                   │  Python     │
    │  (机审服务)  │                   │  (机审服务)  │
    └─────────────┘                   └─────────────┘
```

**关键配置：**
- 使用 Kubernetes 或 Docker Swarm 进行容器编排
- 数据库使用主从复制 + 读写分离
- Redis 使用 Cluster 模式
- 服务无状态，支持水平扩展

## 性能优化建议

### 1. 数据库优化

**索引优化：**
```sql
-- 任务表常用查询索引
CREATE INDEX idx_tasks_status ON moderation_tasks(current_status);
CREATE INDEX idx_tasks_assigned ON moderation_tasks(assigned_to);
CREATE INDEX idx_tasks_video ON moderation_tasks(video_id);
CREATE INDEX idx_tasks_sla ON moderation_tasks(sla_deadline);
CREATE INDEX idx_tasks_created ON moderation_tasks(created_at);

-- 复合索引
CREATE INDEX idx_tasks_status_priority ON moderation_tasks(current_status, priority);
CREATE INDEX idx_tasks_assigned_status ON moderation_tasks(assigned_to, current_status);
```

**查询优化：**
- 分页查询使用 LIMIT + OFFSET 或游标分页
- 大表查询考虑分区（按日期、按状态）
- 热点数据使用 Redis 缓存

### 2. 缓存策略

**缓存层级：**
1. **本地缓存**：Go 使用 `sync.Map` 或 `ristretto` 缓存热点配置
2. **Redis 缓存**：
   - 会话缓存（JWT 黑名单）
   - 任务队列
   - 统计数据缓存
   - 违规标签缓存
   - 视频帧分析结果缓存

**缓存失效：**
- 设置合理的 TTL
- 数据更新时主动失效缓存
- 使用版本号处理并发

### 3. 异步处理

**消息队列：**
- 视频抽帧任务
- 机审分析任务
- 批量操作任务
- 统计数据更新
- 通知发送

**异步模式：**
```go
// 伪代码示例
func ProcessVideoAsync(videoID uuid.UUID) {
    // 1. 提交到消息队列
    queue.Push("video_process", videoID)
    
    // 2. 立即返回，不等待处理完成
    return
}

// Worker 进程异步处理
func Worker() {
    for {
        msg := queue.Pop("video_process")
        go processVideo(msg.VideoID)
    }
}
```

## 常见问题排查

### 1. 服务无法启动

**检查项：**
- 数据库连接是否正常
- Redis 连接是否正常
- 端口是否被占用
- 环境变量是否正确配置
- 依赖是否完整安装

### 2. 机审任务卡住

**检查项：**
- Python 服务是否正常运行
- FFmpeg 是否安装并在 PATH 中
- 视频 URL 是否可访问
- 任务队列是否阻塞
- 查看日志定位错误

### 3. 前端无法连接后端

**检查项：**
- 后端服务是否运行
- API 端口是否开放
- CORS 配置是否正确
- 代理配置（Vite）是否正确
- JWT Token 是否过期

### 4. 数据库查询慢

**检查项：**
- 是否有合适的索引
- 查询语句是否需要优化
- 是否存在 N+1 查询问题
- 表数据量是否过大
- 连接池配置是否合理

## 生产环境注意事项

1. **安全配置：**
   - 修改默认密码和密钥
   - 启用 HTTPS
   - 配置防火墙规则
   - 定期更新依赖包

2. **监控告警：**
   - 服务健康检查
   - 数据库性能监控
   - Redis 内存监控
   - 业务指标监控（SLA 达标率、审核时效）

3. **日志管理：**
   - 统一日志收集（ELK / Loki）
   - 日志轮转和归档
   - 关键操作审计日志

4. **备份恢复：**
   - 数据库定期备份
   - Redis 数据持久化
   - 备份验证和恢复演练

## 总结

本短视频内容风控与审核平台实现了完整的内容审核流程，包括：

✅ **机审系统**：视频抽帧、图像分析、文本检测、音频分析  
✅ **人审系统**：审核工作台、视频预览、违规标记、批量操作  
✅ **状态流转**：16 种审核状态、完整的状态机设计  
✅ **任务分发**：优先级队列、自动分配、负载均衡  
✅ **视频帧同步**：异步抽帧、智能分析、缓存管理  
✅ **机审回写**：评分机制、自动决策阈值、结果持久化  
✅ **申诉处理**：申诉提交、分配处理、结果反馈  
✅ **SLA 监控**：时效管理、临近提醒、达标率统计  
✅ **统计报表**：仪表盘、趋势图、分布图、绩效分析  

系统采用微服务架构，前后端分离，易于扩展和维护。可以根据实际业务需求接入更强大的 AI 模型，提升内容检测的准确率。
