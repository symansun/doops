import type { AgentConnectionStatus, AgentSessionStatus } from "./contracts.js";
type ActivityStatus = "pending" | "running" | "waiting" | "completed" | "failed" | "cancelled";
export declare function englishSessionStatus(status: AgentSessionStatus, connection: AgentConnectionStatus): "Reconnecting" | "Offline" | "Working" | "Starting" | "Failed" | "Completed" | "Ready";
export declare function chineseSessionStatus(status: AgentSessionStatus, connection: AgentConnectionStatus): "正在重连" | "已离线" | "执行中" | "正在启动" | "执行失败" | "已完成" | "就绪";
export declare function englishActivityStatus(status: ActivityStatus): string;
export declare function chineseActivityStatus(status: ActivityStatus): string;
export {};
