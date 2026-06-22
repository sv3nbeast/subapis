-- 升级返利档位体系：按"累计带来的被邀请人充值额(GMV)"自动跳档。
-- GMV 单调递增，档位只升不降。

-- 1) 给 user_affiliates 增加 GMV 累计列。
ALTER TABLE user_affiliates
    ADD COLUMN IF NOT EXISTS aff_total_invitee_recharge DECIMAL(20,8) NOT NULL DEFAULT 0;

COMMENT ON COLUMN user_affiliates.aff_total_invitee_recharge IS 'Cumulative recharge face value brought in by this user''s invitees; drives auto rebate tier';

-- 2) 回填历史 GMV。
-- ledger 里存在 accrue 流水即表示当时订单已结算（返利仅在订单 COMPLETED 时产生），
-- 因此直接对关联订单的面值(po.amount)求和，无需再按 status 过滤。
-- 只能回填有 source_order_id 的订单充值；redeem 兑换无 order 关联，历史部分无法精确回填（可接受）。
UPDATE user_affiliates ua
SET aff_total_invitee_recharge = COALESCE(sub.total, 0)
FROM (
    SELECT ual.user_id, SUM(po.amount) AS total
    FROM user_affiliate_ledger ual
    JOIN payment_orders po ON po.id = ual.source_order_id
    WHERE ual.action = 'accrue' AND ual.source_order_id IS NOT NULL
    GROUP BY ual.user_id
) sub
WHERE ua.user_id = sub.user_id;
