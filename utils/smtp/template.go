package smtp

const VerificationCodeTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <title>您的Haruki工具箱验证码</title>
</head>
<body style="font-family: Arial, sans-serif; background-color: #f7f7f7; padding: 20px;">
  <table align="center" width="600" cellpadding="0" cellspacing="0" 
         style="background-color: #ffffff; border-radius: 8px; padding: 20px;">
    <tr>
      <td style="text-align: center; font-size: 20px; font-weight: bold; color: #333;">
        Haruki工具箱 | 验证邮箱
      </td>
    </tr>
    <tr>
      <td style="padding: 20px; text-align: center; font-size: 16px; color: #555;">
        您正在进行邮箱验证操作，请输入以下验证码完成验证：
      </td>
    </tr>
    <tr>
      <td style="text-align: center; padding: 20px;">
        <div style="display: inline-block; font-size: 28px; font-weight: bold; 
                    letter-spacing: 8px; color: #2c3e50; 
                    background-color: #f1f1f1; padding: 12px 24px; 
                    border-radius: 6px; border: 1px solid #ddd;">
          {{CODE}}
        </div>
      </td>
    </tr>
    <tr>
      <td style="padding: 20px; text-align: center; font-size: 14px; color: #999;">
        该验证码有效期为 <b>5 分钟</b>，请勿泄露给他人。
      </td>
    </tr>
  </table>
</body>
</html>
`
const ResetPasswordTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <title>Haruki工具箱 | 重置密码</title>
</head>
<body style="font-family: Arial, sans-serif; background-color: #f7f7f7; padding: 20px;">
  <table align="center" width="600" cellpadding="0" cellspacing="0"
         style="background-color: #ffffff; border-radius: 8px; padding: 20px;">
    <tr>
      <td style="text-align: center; font-size: 20px; font-weight: bold; color: #333;">
        Haruki工具箱 | 重置密码
      </td>
    </tr>
    <tr>
      <td style="padding: 20px; text-align: center; font-size: 16px; color: #555;">
        您正在进行密码重置操作，请点击以下按钮完成重置：
      </td>
    </tr>
    <tr>
      <td style="text-align: center; padding: 20px;">
        <a href="{{LINK}}" style="display: inline-block; padding: 15px 30px; background-color: #4CAF50; color: #ffffff; border-radius: 6px; font-weight: bold; font-size: 20px; text-decoration: none;">
          重置密码
        </a>
      </td>
    </tr>
    <tr>
      <td style="padding: 10px 20px 20px 20px; font-size: 14px; color: #555; text-align: center;">
        如果无法打开链接，请复制以下地址到浏览器打开：
      </td>
    </tr>
    <tr>
      <td style="padding: 0 20px 20px 20px; text-align: center;">
        <div style="font-family: monospace; font-size: 12px; border: 1px solid #ddd; padding: 10px; word-break: break-all; display: inline-block; max-width: 100%;">
          {{LINK}}
        </div>
      </td>
    </tr>
  </table>
</body>
</html>
`
