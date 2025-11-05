package smtp

const VerificationCodeTemplate = `<!DOCTYPE html>
<html lang="zh-CN">

<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>您的Haruki工具箱验证码</title>
</head>

<body
    style="margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; background-color: #fafafa; padding: 48px 16px;">
    <table align="center" width="100%" cellpadding="0" cellspacing="0" style="max-width: 580px; margin: 0 auto;">
        <tr>
            <td>
                <table width="100%" cellpadding="0" cellspacing="0"
                    style="background-color: #ffffff; border-radius: 12px; box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04), 0 1px 3px rgba(0, 0, 0, 0.06); border: 1px solid #e5e7eb; overflow: hidden;">
                    <tr>
                        <td style="padding: 40px 40px 24px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0" border="0">
                                <tr>
                                    <td style="text-align:left;">
                                        <table cellpadding="0" cellspacing="0" border="0"
                                            style="border-collapse:collapse;">
                                            <tr valign="middle">
                                                <!-- Logo -->
                                                <td style="padding-right:12px; vertical-align:middle;">
                                                    <img src="https://haruki.seiunx.com/haruki.ico" alt="Logo"
                                                        height="32"
                                                        style="display:block; border:0; outline:none; text-decoration:none; width:auto;">
                                                </td>

                                                <!-- 标题 -->
                                                <td
                                                    style="font-size:18px; font-weight:600; color:#18181b; letter-spacing:-0.01em; vertical-align:middle;">
                                                    Haruki工具箱
                                                </td>
                                            </tr>
                                        </table>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <tr>
                        <td style="padding: 0 40px 8px 40px; text-align: center;">
                            <h1
                                style="margin: 0; font-size: 24px; font-weight: 600; color: #18181b; letter-spacing: -0.02em; line-height: 1.3;">
                                验证您的邮箱
                            </h1>
                        </td>
                    </tr>

                    <tr>
                        <td style="padding: 0 40px 32px 40px; text-align: center;">
                            <p style="margin: 0; font-size: 15px; color: #71717a; line-height: 1.6;">
                                我们收到了您的验证邮箱的请求，<br>请使用以下验证码完成您的邮箱验证，<br>验证码将在 5 分钟后过期。
                            </p>
                        </td>
                    </tr>

                    <tr>
                        <td style="padding: 0 40px 24px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0"
                                style="background-color: #fafafa; border-radius: 8px; border: 1px solid #e5e7eb;">
                                <tr>
                                    <td style="padding: 24px; text-align: center;">
                                        <div
                                            style="font-size: 14px; font-weight: 500; color: #52525b; margin-bottom: 12px; text-transform: uppercase; letter-spacing: 0.05em;">
                                            验证码
                                        </div>
                                        <div
                                            style="font-family: 'SF Mono', Monaco, 'Cascadia Code', 'Roboto Mono', Consolas, 'Courier New', monospace; font-size: 32px; font-weight: 600; letter-spacing: 8px; color: #18181b; user-select: all;">
                                            {{CODE}}
                                        </div>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <tr>
                        <td style="padding: 0 40px 32px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0"
                                style="background-color: #fef3c7; border-radius: 8px; border: 1px solid #fcd34d;">
                                <tr>
                                    <td style="padding: 12px 16px;">
                                        <table width="100%" cellpadding="0" cellspacing="0">
                                            <tr>
                                                <td style="width: 20px; vertical-align: top; padding-top: 2px;">
                                                    <img src="https://haruki.seiunx.com/triangle-alert.png" alt="Alert"
                                                        width="20" height="20"
                                                        style="display:block; border:0; outline:none; text-decoration:none;">
                                                </td>
                                                <td style="padding-left: 8px;">
                                                    <p
                                                        style="margin: 0; font-size: 14px; color: #78350f; line-height: 1.5;">
                                                        如果您没有请求此验证码，请忽略此邮件。
                                                    </p>
                                                </td>
                                            </tr>
                                        </table>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>
                    <tr>
                        <td style="padding: 0 40px;">
                            <div style="height: 1px; background-color: #e5e7eb;"></div>
                        </td>
                    </tr>

                    <tr>
                        <td style="padding: 24px 40px 32px 40px; text-align: center;">
                            <p style="margin: 0 0 8px 0; font-size: 13px; color: #a1a1aa; line-height: 1.5;">
                                此邮件由 Haruki工具箱 自动发送，请勿直接回复。
                            </p>
                            <p style="margin: 0; font-size: 13px; color: #d4d4d8; text-align: center;">
                                © 2025 Haruki工具箱. All rights reserved.
                            </p>
                        </td>
                    </tr>
                </table>

                <table width="100%" cellpadding="0" cellspacing="0" style="margin-top: 32px;">
                    <!--<tr>
            <td style="text-align: center;">
              <p style="margin: 0; font-size: 12px; color: #a1a1aa;">
                <a href="#" style="color: #71717a; text-decoration: none; margin: 0 8px;">帮助中心</a>
                <span style="color: #d4d4d8;">•</span>
                <a href="#" style="color: #71717a; text-decoration: none; margin: 0 8px;">隐私政策</a>
                <span style="color: #d4d4d8;">•</span>
                <a href="#" style="color: #71717a; text-decoration: none; margin: 0 8px;">服务条款</a>
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr> -->
                </table>
</body>

</html>
`

const ResetPasswordTemplate = `<!DOCTYPE html>
<html lang="zh-CN">

<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Haruki工具箱 | 重置密码</title>
</head>

<body
    style="margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; background-color: #fafafa; padding: 48px 16px;">
    <table align="center" width="100%" cellpadding="0" cellspacing="0" style="max-width: 580px; margin: 0 auto;">
        <tr>
            <td>
                <table width="100%" cellpadding="0" cellspacing="0"
                    style="background-color: #ffffff; border-radius: 12px; box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04), 0 1px 3px rgba(0, 0, 0, 0.06); border: 1px solid #e5e7eb; overflow: hidden;">

                    <!-- Header -->
                    <tr>
                        <td style="padding: 40px 40px 24px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0" border="0">
                                <tr>
                                    <td style="text-align:left;">
                                        <table cellpadding="0" cellspacing="0" border="0"
                                            style="border-collapse:collapse;">
                                            <tr valign="middle">
                                                <td style="padding-right:12px; vertical-align:middle;">
                                                    <img src="https://haruki.seiunx.com/haruki.ico" alt="Logo"
                                                        height="32"
                                                        style="display:block; border:0; outline:none; text-decoration:none; width:auto;">
                                                </td>
                                                <td
                                                    style="font-size:18px; font-weight:600; color:#18181b; letter-spacing:-0.01em; vertical-align:middle;">
                                                    Haruki工具箱
                                                </td>
                                            </tr>
                                        </table>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- Title -->
                    <tr>
                        <td style="padding: 0 40px 8px 40px; text-align: center;">
                            <h1
                                style="margin: 0; font-size: 24px; font-weight: 600; color: #18181b; letter-spacing: -0.02em; line-height: 1.3;">
                                重置您的密码
                            </h1>
                        </td>
                    </tr>

                    <!-- Description -->
                    <tr>
                        <td style="padding: 0 40px 32px 40px; text-align: center;">
                            <p style="margin: 0; font-size: 15px; color: #71717a; line-height: 1.6;">
                                我们收到了您的密码重置请求，<br>点击下方按钮即可设置新密码，<br>此链接将在 30 分钟后过期。
                            </p>
                        </td>
                    </tr>

                    <!-- Button -->
                    <tr>
                        <td style="padding: 0 40px 24px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0">
                                <tr>
                                    <td>
                                        <a href="{{LINK}}"
                                            style="display: inline-block; width: 100%; padding: 12px 24px; background-color: #18181b; color: #ffffff; text-decoration: none; font-weight: 500; font-size: 15px; border-radius: 8px; text-align: center; box-sizing: border-box;">
                                            重置密码
                                        </a>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- OR Divider -->
                    <tr>
                        <td style="padding: 0 40px 16px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0" border="0"
                                style="border-collapse: collapse;">
                                <tr style="vertical-align: middle;">
                                    <!-- 左边线 -->
                                    <td style="width: 45%; vertical-align: middle;">
                                        <div style="border-top: 0.5px solid #e5e7eb; height: 0; line-height: 0;">&nbsp;
                                        </div>
                                    </td>

                                    <!-- 中间 “或” -->
                                    <td
                                        style="width: 10%; text-align: center; padding: 0 12px; vertical-align: middle;">
                                        <span style="font-size: 12px; color: #a1a1aa; white-space: nowrap;">或</span>
                                    </td>

                                    <!-- 右边线 -->
                                    <td style="width: 45%; vertical-align: middle;">
                                        <div style="border-top: 0.5px solid #e5e7eb; height: 0; line-height: 0;">&nbsp;
                                        </div>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>



                    <!-- Link fallback -->
                    <tr>
                        <td style="padding: 0 40px 32px 40px;">
                            <p style="margin: 0 0 12px 0; font-size: 13px; color: #71717a; text-align: center;">
                                如果按钮无法使用，请复制以下链接：
                            </p>
                            <table width="100%" cellpadding="0" cellspacing="0"
                                style="background-color: #fafafa; border-radius: 6px; border: 1px solid #e5e7eb;">
                                <tr>
                                    <td style="padding: 12px 16px;">
                                        <p
                                            style="margin: 0; font-family: 'SF Mono', Monaco, 'Cascadia Code', 'Roboto Mono', Consolas, 'Courier New', monospace; font-size: 12px; color: #52525b; word-break: break-all; line-height: 1.6;">
                                            {{LINK}}
                                        </p>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- Security Notice (updated style to match first template) -->
                    <tr>
                        <td style="padding: 0 40px 32px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0"
                                style="background-color: #fef3c7; border-radius: 8px; border: 1px solid #fcd34d;">
                                <tr>
                                    <td style="padding: 12px 16px;">
                                        <table width="100%" cellpadding="0" cellspacing="0">
                                            <tr>
                                                <td style="width: 20px; vertical-align: top; padding-top: 2px;">
                                                    <img src="https://haruki.seiunx.com/triangle-alert.png" alt="Alert"
                                                        width="20" height="20"
                                                        style="display:block; border:0; outline:none; text-decoration:none;">
                                                </td>
                                                <td style="padding-left: 8px;">
                                                    <p
                                                        style="margin: 0; font-size: 14px; color: #78350f; line-height: 1.5;">
                                                        如果您没有请求重置密码，请忽略此邮件。您的账户仍然安全。
                                                    </p>
                                                </td>
                                            </tr>
                                        </table>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- Footer -->
                    <tr>
                        <td style="padding: 0 40px;">
                            <div style="height: 1px; background-color: #e5e7eb;"></div>
                        </td>
                    </tr>

                    <tr>
                        <td style="padding: 24px 40px 32px 40px; text-align: center;">
                            <p style="margin: 0 0 8px 0; font-size: 13px; color: #a1a1aa; line-height: 1.5;">
                                此邮件由 Haruki工具箱 自动发送，请勿直接回复。
                            </p>
                            <p style="margin: 0; font-size: 13px; color: #d4d4d8; text-align: center;">
                                © 2025 Haruki工具箱. All rights reserved.
                            </p>
                        </td>
                    </tr>

                </table>
            </td>
        </tr>
    </table>
</body>

</html>
`
