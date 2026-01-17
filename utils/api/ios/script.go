package ios

const IOSJavaScriptTemplate = `// Modified from yichahucha & NobyDa
function HarukiUploadClient() {
    const start = Date.now()
    const isRequest = typeof $request != "undefined"
    const isSurge = typeof $httpClient != "undefined"
    const isQuanX = typeof $task != "undefined"
    const isLoon = typeof $loon != "undefined"
    const isJSBox = typeof $app != "undefined" && typeof $http != "undefined"
    const isNode = typeof require == "function" && !isJSBox;
    const NodeSet = 'CookieSet.json'
    const node = (() => {
        if (isNode) {
            const request = require('request');
            const fs = require("fs");
            const path = require("path");
            return ({
                request,
                fs,
                path
            })
        } else {
            return (null)
        }
    })()
    const notify = (title, subtitle, message, rawopts) => {
        const Opts = (rawopts) => { //Modified from https://github.com/chavyleung/scripts/blob/master/Env.js
            if (!rawopts) return rawopts
            if (typeof rawopts === 'string') {
                if (isLoon) return rawopts
                else if (isQuanX) return {
                    'open-url': rawopts
                }
                else if (isSurge) return {
                    url: rawopts
                }
                else return undefined
            } else if (typeof rawopts === 'object') {
                if (isLoon) {
                    let openUrl = rawopts.openUrl || rawopts.url || rawopts['open-url']
                    let mediaUrl = rawopts.mediaUrl || rawopts['media-url']
                    return {
                        openUrl,
                        mediaUrl
                    }
                } else if (isQuanX) {
                    let openUrl = rawopts['open-url'] || rawopts.url || rawopts.openUrl
                    let mediaUrl = rawopts['media-url'] || rawopts.mediaUrl
                    return {
                        'open-url': openUrl,
                        'media-url': mediaUrl
                    }
                } else if (isSurge) {
                    let openUrl = rawopts.url || rawopts.openUrl || rawopts['open-url']
                    return {
                        url: openUrl
                    }
                }
            } else {
                return undefined
            }
        }
        console.log(\u0060${title}\n${subtitle}\n${message}\u0060)
        if (isQuanX) $notify(title, subtitle, message, Opts(rawopts))
        if (isSurge) $notification.post(title, subtitle, message, Opts(rawopts))
        if (isJSBox) $push.schedule({
            title: title,
            body: subtitle ? subtitle + "\n" + message : message
        })
    }
    const write = (value, key) => {
        if (isQuanX) return $prefs.setValueForKey(value, key)
        if (isSurge) return $persistentStore.write(value, key)
        if (isNode) {
            try {
                if (!node.fs.existsSync(node.path.resolve(__dirname, NodeSet)))
                    node.fs.writeFileSync(node.path.resolve(__dirname, NodeSet), JSON.stringify({}));
                const dataValue = JSON.parse(node.fs.readFileSync(node.path.resolve(__dirname, NodeSet)));
                if (value) dataValue[key] = value;
                if (!value) delete dataValue[key];
                return node.fs.writeFileSync(node.path.resolve(__dirname, NodeSet), JSON.stringify(dataValue));
            } catch (er) {
                return AnError('Node.js持久化写入', null, er);
            }
        }
        if (isJSBox) {
            if (!value) return $file.delete(\u0060shared://${key}.txt\u0060);
            return $file.write({
                data: $data({
                    string: value
                }),
                path: \u0060shared://${key}.txt\u0060
            })
        }
    }
    const read = (key) => {
        if (isQuanX) return $prefs.valueForKey(key)
        if (isSurge) return $persistentStore.read(key)
        if (isNode) {
            try {
                if (!node.fs.existsSync(node.path.resolve(__dirname, NodeSet))) return null;
                const dataValue = JSON.parse(node.fs.readFileSync(node.path.resolve(__dirname, NodeSet)))
                return dataValue[key]
            } catch (er) {
                return AnError('Node.js持久化读取', null, er)
            }
        }
        if (isJSBox) {
            if (!$file.exists(\u0060shared://${key}.txt\u0060)) return null;
            return $file.read(\u0060shared://${key}.txt\u0060).string
        }
    }
    const adapterStatus = (response) => {
        if (response) {
            if (response.status) {
                response["statusCode"] = response.status
            } else if (response.statusCode) {
                response["status"] = response.statusCode
            }
        }
        return response
    }
    const get = (options, callback) => {
        options.headers['User-Agent'] = 'HarukiScriptClient/v1.0.0'
        if (isQuanX) {
            if (typeof options == "string") options = {
                url: options
            }
            options["method"] = "GET"
            //options["opts"] = {
            //  "hints": false
            //}
            $task.fetch(options).then(response => {
                callback(null, adapterStatus(response), response.body)
            }, reason => callback(reason.error, null, null))
        }
        if (isSurge) {
            options.headers['X-Surge-Skip-Scripting'] = false
            $httpClient.get(options, (error, response, body) => {
                callback(error, adapterStatus(response), body)
            })
        }
        if (isNode) {
            node.request(options, (error, response, body) => {
                callback(error, adapterStatus(response), body)
            })
        }
        if (isJSBox) {
            if (typeof options == "string") options = {
                url: options
            }
            options["header"] = options["headers"]
            options["handler"] = function (resp) {
                let error = resp.error;
                if (error) error = JSON.stringify(resp.error)
                let body = resp.data;
                if (typeof body == "object") body = JSON.stringify(resp.data);
                callback(error, adapterStatus(resp.response), body)
            };
            $http.get(options);
        }
    }
    const post = (options, callback) => {
        options.headers['User-Agent'] = 'HarukiScriptClient/v1.0.0'
        if (options.body) options.headers['Content-Type'] = 'application/octet-stream'
        if (isQuanX) {
            if (typeof options == "string") options = {
                url: options
            }
            options["method"] = "POST"
            //options["opts"] = {
            //  "hints": false
            //}
            $task.fetch(options).then(response => {
                callback(null, adapterStatus(response), response.body)
            }, reason => callback(reason.error, null, null))
        }
        if (isSurge) {
            options.headers['X-Surge-Skip-Scripting'] = false
            $httpClient.post(options, (error, response, body) => {
                callback(error, adapterStatus(response), body)
            })
        }
        if (isNode) {
            node.request.post(options, (error, response, body) => {
                callback(error, adapterStatus(response), body)
            })
        }
        if (isJSBox) {
            if (typeof options == "string") options = {
                url: options
            }
            options["header"] = options["headers"]
            options["handler"] = function (resp) {
                let error = resp.error;
                if (error) error = JSON.stringify(resp.error)
                let body = resp.data;
                if (typeof body == "object") body = JSON.stringify(resp.data)
                callback(error, adapterStatus(resp.response), body)
            }
            $http.post(options);
        }
    }
    const AnError = (name, keyname, er, resp, body) => {
        if (typeof (merge) != "undefined" && keyname) {
            if (!merge[keyname].notify) {
                merge[keyname].notify = \u0060${name}: 异常, 已输出日志 ‼️\u0060
            } else {
                merge[keyname].notify += \u0060\n${name}: 异常, 已输出日志 ‼️ (2)\u0060
            }
            merge[keyname].error = 1
        }
        return console.log(\u0060\n‼️${name}发生错误\n‼️名称: ${er.name}\n‼️描述: ${er.message}${JSON.stringify(er).match(/\"line\"/) ? \u0060\n‼️行列: ${JSON.stringify(er)}\u0060 : \u0060\u0060}${resp && resp.status ? \u0060\n‼️状态: ${resp.status}\u0060 : \u0060\u0060}${body ? \u0060\n‼️响应: ${resp && resp.status != 503 ? body : \u0060Omit.\u0060}\u0060 : \u0060\u0060}\u0060)
    }
    const time = () => {
        const end = ((Date.now() - start) / 1000).toFixed(2)
        return console.log('\n签到用时: ' + end + ' 秒')
    }
    const done = (value = {}) => {
        if (isQuanX) return $done(value)
        if (isSurge) isRequest ? $done(value) : $done()
    }
    return {
        AnError,
        isRequest,
        isJSBox,
        isSurge,
        isQuanX,
        isLoon,
        isNode,
        notify,
        write,
        read,
        get,
        post,
        time,
        done
    }
}

// Script to upload game api response body in chunks
// Original author: NeuraXmy
// Multiple platforms supported modified by Haruki Dev Team
// Generated by Haruki Toolbox
// Generated at {{GENERATE_DATE}}
const $ = HarukiUploadClient();
const scriptName = "haruki_toolbox_uploader.js";
const version = "1.0.0";

const upload_id = Math.random().toString(36).substr(2, 9);
const upload_url = "{{UPLOAD_URL}}";
const chunkSize = {{CHUNK_SIZE}} * 1024 * 1024; // {{CHUNK_SIZE}}MB per chunk
const body = typeof $response !== 'undefined' ? $response.body : '';
const url = $.isRequest ? $request.url : '';
const totalChunks = Math.ceil(body.length / chunkSize);

let sentChunks = 0;
let failedChunks = 0;

function log(message) {
    console.log(\u0060[${scriptName}-v${version}] [${upload_id}] ${message}\u0060);
}

log(\u0060start to upload response body with ${totalChunks} chunks\u0060);
log(\u0060body length: ${body.length}\u0060);
log(\u0060original url: ${url}\u0060);

function sendChunk(index) {
    const start = index * chunkSize;
    const end = Math.min(start + chunkSize, body.length);
    const chunk = body.slice(start, end);

    const options = {
        url: upload_url,
        headers: {
            "X-Script-Version": version,
            "X-Original-Url": url,
            "X-Upload-Id": upload_id,
            "X-Chunk-Index": index,
            "X-Total-Chunks": totalChunks,
            "X-Upload-Code": "{{UPLOAD_CODE}}",
            "Content-Type": "application/octet-stream",
        },
        body: chunk
    };

    log(\u0060uploading chunk ${index + 1}/${totalChunks} (${chunk.length} bytes)\u0060);

    $.post(options, (error, resp, data) => {
        sentChunks++;
        if (error) {
            log(\u0060chunk ${index + 1} upload failed: ${error}\u0060);
            failedChunks++;
        } else if (resp.status !== 200) {
            log(\u0060chunk ${index + 1} upload failed: HTTP ${resp.status}\u0060);
            failedChunks++;
        } else {
            log(\u0060chunk ${index + 1} upload success\u0060);
        }

        if (sentChunks === totalChunks) {
            log(\u0060upload completed: ${failedChunks}/${sentChunks} chunks failed\u0060);
            $.done({});
        } else {
            sendChunk(index + 1);
        }
    });
}

if ($.isRequest && typeof $response !== 'undefined') {
    sendChunk(0);
}
`
