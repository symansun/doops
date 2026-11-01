// Gateway 的登录响应和 /v1/targets 都不带 is_admin。控制台用一个只有平台管理员
// 才能读的接口探测：200 是管理员，其它结果（403、401、404、5xx、跳转、网络错误）
// 都不当作管理员。结果只决定界面是否显示管理入口，权限仍由 Gateway 检查。
export async function probeGatewayAdmin(gateway: string, token: string): Promise<boolean> {
  try {
    const response = await fetch(`${gateway}/v1/admin/instances`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
      redirect: "manual",
    })
    await response.body?.cancel()
    return response.status === 200
  } catch {
    return false
  }
}
