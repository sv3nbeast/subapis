# Public UI v2 平滑切换方案

## 边界

公共 UI v2 只替换登录前页面的视觉外壳和排版。登录后控制台继续由
`VITE_UI_V2_ROLLOUT_MODE` 独立控制，两套灰度互不影响。

旧版公共页面在预览阶段始终是默认界面。管理员自定义首页、Stripe 弹窗以及
Stripe/Airwallex SDK 挂载页不进入公共 UI v2 外壳。

## 构建参数

| 参数 | 可选值 | 默认值 | 行为 |
| --- | --- | --- | --- |
| `VITE_PUBLIC_UI_V2_ROLLOUT_MODE` | `off` / `preview` / `percentage` / `full` | `preview` | 控制公共新版启用范围 |
| `VITE_PUBLIC_UI_V2_ROLLOUT_PERCENT` | `0` - `100` | `0` | `percentage` 模式下的稳定访客比例 |

访客会在浏览器中获得独立、稳定的匿名分桶标识。该标识不包含账号或设备信息。

## 预览与回退

- 启用新版：访问任意已接入的公共页面并添加 `?public_ui=v2`。
- 回退旧版：添加 `?public_ui=legacy`。
- 选择保存在 `sub2api:public-ui-version:*`，不会修改控制台 UI 偏好。
- URL 中的 `public_ui` 在偏好持久化后自动移除，其他 query 和 hash 原样保留。

## 推荐灰度

1. 本地与测试环境使用 `preview`，覆盖桌面、手机、亮色、暗色和减少动态效果。
2. 生产首次包含代码时仍使用 `preview`，只由内部测试人员显式进入。
3. 稳定后切到 `percentage`，按 5%、25%、50%、100% 逐级观察。
4. 最后切换 `full`，并至少保留一个发布周期的 `public_ui=legacy` 回退能力。

任何阶段出现认证入口不可达、OAuth 回调异常、支付结果恢复异常、布局溢出或页面
性能明显退化时，重新以 `off` 构建即可让所有访客返回旧版。公共 UI v2 不包含数据库迁移。
