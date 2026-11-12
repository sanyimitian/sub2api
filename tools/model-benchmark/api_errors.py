"""Safe error summaries for result cards; never export raw upstream messages."""
import re

API_ERROR_CODES = ('http_error', 'request_error', 'response_error', 'cancelled', 'unknown')


def summarize_api_errors(results):
    groups = {}
    for result in results:
        if result.get('status') != 'api_error':
            continue
        status = result.get('http_status')
        code = result.get('api_error_code')
        message = result.get('message', '')
        if type(status) is not int or not 300 <= status <= 599:
            # Historical runs already contain this curated message, not a raw body.
            match = re.match(r'^API 返回 HTTP ([3-5][0-9]{2})(?:$|：)', message) if isinstance(message, str) else None
            status = int(match[1]) if match else None
        if status is not None:
            code = 'http_error'
        elif code not in API_ERROR_CODES or code == 'http_error':
            code = {
                '测试已停止': 'cancelled',
                'API 请求超时、连接中断或响应格式不正确': 'request_error',
                '无法连接 API 服务': 'request_error',
                '无法解析 API 域名': 'request_error',
                'API 地址不能指向本机或内网': 'request_error',
                'API 返回格式不正确': 'response_error',
                'API 响应超过大小限制': 'response_error',
                '模型响应状态异常或响应格式不正确': 'response_error',
                '模型响应未完成、被截断或没有返回答案': 'response_error',
            }.get(message, 'unknown') if isinstance(message, str) else 'unknown'
        key = (code, status)
        if key not in groups:
            groups[key] = {'code': code, 'count': 0}
            if status is not None:
                groups[key]['http_status'] = status
        groups[key]['count'] += 1
    return sorted(groups.values(), key=lambda item: (-item['count'], item['code'], item.get('http_status', 0)))


def sanitize_api_error_details(value, api_errors):
    if not isinstance(value, list) or len(value) > 100:
        raise ValueError('Invalid API error details')
    output, seen, total = [], set(), 0
    for item in value:
        if not isinstance(item, dict) or item.get('code') not in API_ERROR_CODES:
            raise ValueError('Invalid API error code')
        code, count, status = item['code'], item.get('count'), item.get('http_status')
        if type(count) is not int or not 1 <= count <= 100:
            raise ValueError('Invalid API error count')
        clean = {'code': code, 'count': count}
        if code == 'http_error':
            if type(status) is not int or not 300 <= status <= 599:
                raise ValueError('Invalid HTTP status')
            clean['http_status'] = status
        elif status is not None:
            raise ValueError('Unexpected HTTP status')
        key = (code, status)
        if key in seen:
            raise ValueError('Duplicate API error detail')
        seen.add(key)
        total += count
        output.append(clean)
    if total > api_errors:
        raise ValueError('Inconsistent API error counts')
    return output
