package ios

import (
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
)

var IOSMitMHostnameMapping map[utils.SupportedDataUploadServer][]string = map[utils.SupportedDataUploadServer][]string{
	utils.SupportedDataUploadServerJP: {harukiConfig.Cfg.SekaiClient.JPServerAPIHost},
	utils.SupportedDataUploadServerEN: {harukiConfig.Cfg.SekaiClient.ENServerAPIHost},
	utils.SupportedDataUploadServerTW: {harukiConfig.Cfg.SekaiClient.TWServerAPIHost, harukiConfig.Cfg.SekaiClient.TWServerAPIHost2},
	utils.SupportedDataUploadServerKR: {harukiConfig.Cfg.SekaiClient.KRServerAPIHost, harukiConfig.Cfg.SekaiClient.KRServerAPIHost2},
	utils.SupportedDataUploadServerCN: {harukiConfig.Cfg.SekaiClient.CNServerAPIHost, harukiConfig.Cfg.SekaiClient.CNServerAPIHost2},
}
var IOSMitMHostnameReverseMapping map[string]utils.SupportedDataUploadServer = map[string]utils.SupportedDataUploadServer{
	harukiConfig.Cfg.SekaiClient.JPServerAPIHost:  utils.SupportedDataUploadServerJP,
	harukiConfig.Cfg.SekaiClient.ENServerAPIHost:  utils.SupportedDataUploadServerEN,
	harukiConfig.Cfg.SekaiClient.TWServerAPIHost:  utils.SupportedDataUploadServerTW,
	harukiConfig.Cfg.SekaiClient.TWServerAPIHost2: utils.SupportedDataUploadServerTW,
	harukiConfig.Cfg.SekaiClient.KRServerAPIHost:  utils.SupportedDataUploadServerKR,
	harukiConfig.Cfg.SekaiClient.KRServerAPIHost2: utils.SupportedDataUploadServerKR,
	harukiConfig.Cfg.SekaiClient.CNServerAPIHost:  utils.SupportedDataUploadServerCN,
	harukiConfig.Cfg.SekaiClient.CNServerAPIHost2: utils.SupportedDataUploadServerCN,
}

const IOSSurgeModuleTemplate = `#!name=合成大Haruki工具箱上传数据模块
#!desc=本模块用于自动获取选定区服与选定数据类型的数据，并上传至Haruki工具箱
#!homepage=https://haruki.seiunx.com/ios-modules
#!author=Haruki Dev Team

[MITM]
hostname=  %APPEND% {{HOSTNAME}}, submit.backtrace.io

[URL Rewrite]
{{URL_POLICY}}
^https:\/\/submit\.backtrace\.io\/ reject`

const IOSQuantumultXModuleTemplate = `{{URL_POLICY}}
^https:\/\/submit\.backtrace\.io\/ url reject

hostname = {{HOSTNAME}}, submit.backtrace.io`

const IOSLoonModuleTemplate = `#!name= 合成大Haruki工具箱上传数据模块
#!desc= 本模块用于自动获取选定区服与选定数据类型的数据，并上传至Haruki工具箱
#!homepage= https://haruki.seiunx.com/ios-modules
#!author= Haruki Dev Team

[Rewrite]
{{URL_POLICY}}
^https:\/\/submit\.backtrace\.io\/ reject

[MITM]
hostname= {{HOSTNAME}}, submit.backtrace.io`

const IOSStashModuleTemplate = `name: 合成大Haruki工具箱上传数据模块
desc: 本模块用于自动获取选定区服与选定数据类型的数据，并上传至Haruki工具箱
# author: Haruki Dev Team
# repo: https://haruki.seiunx.com/ios-modules
# redirect: 2
# mitm: 2
# total: 4

http:
  rewrite:
    {{URL_POLICY}}
    - ^https:\/\/submit\.backtrace\.io\/ - reject

  mitm:
    {{HOSTNAME}}
    - "submit.backtrace.io"`
