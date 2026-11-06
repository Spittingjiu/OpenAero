module("luci.controller.openaero", package.seeall)

function index()
	if not nixio.fs.access("/etc/config/openaero") then
		return
	end

	-- Secure in-session local REST Proxy & RPC (Resolves SEC-03 and UI-04 HTTPS Mixed-Content)
	entry({"admin", "services", "openaero", "proxy"}, call("action_proxy")).leaf = true

	-- If modern LuCI JSON menu exists (/usr/share/luci/menu.d/luci-app-openaero.json),
	-- avoid registering duplicate menu entries in Lua controller
	if nixio.fs.access("/usr/share/luci/menu.d/luci-app-openaero.json") then
		return
	end

	-- Fallback for legacy OpenWrt / LuCI firmware without menu.d support
	entry({"admin", "services", "openaero"}, alias("admin", "services", "openaero", "overview"), _("OpenAero"), 50)
	local e1 = entry({"admin", "services", "openaero", "overview"}, view("openaero/overview"), _("📊 运行概览"), 10)
	e1.leaf = true
	local e2 = entry({"admin", "services", "openaero", "nodes"}, view("openaero/overview"), _("🌐 节点与分流"), 20)
	e2.leaf = true
	local e3 = entry({"admin", "services", "openaero", "rules"}, view("openaero/overview"), _("🛡️ 规则与设备"), 30)
	e3.leaf = true
	local e4 = entry({"admin", "services", "openaero", "settings"}, view("openaero/overview"), _("⚙️ 系统与订阅"), 40)
	e4.leaf = true
end

function action_proxy()
	local http = require("luci.http")
	local nixio = require("nixio")
	
	local path = http.formvalue("path") or ""
	if not path:match("^/") then
		path = "/" .. path
	end
	
	local method = http.getenv("REQUEST_METHOD") or "GET"
	local post_data = http.source() and http.content() or ""
	
	local sock = nixio.connect("127.0.0.1", 2026)
	if not sock then
		http.status(502, "Bad Gateway")
		http.prepare_content("application/json")
		http.write_json({ error = "openaero-core daemon unreachable on 127.0.0.1:2026" })
		return
	end
	
	local token = ""
	local tf = io.open("/etc/openaero/.api_token", "r")
	if tf then
		token = tf:read("*a") or ""
		tf:close()
		token = token:gsub("%s+", "")
	end

	local token_header = ""
	if token ~= "" then
		token_header = string.format("X-OpenAero-Token: %s\r\n", token)
	end

	local req = string.format("%s %s HTTP/1.0\r\nHost: 127.0.0.1:2026\r\nConnection: close\r\nContent-Type: application/json\r\n%sContent-Length: %d\r\n\r\n%s",
		method, path, token_header, #post_data, post_data)
	sock:send(req)
	
	local response = ""
	while true do
		local chunk = sock:recv(4096)
		if not chunk or #chunk == 0 then break end
		response = response .. chunk
	end
	sock:close()
	
	local header_end = response:find("\r\n\r\n")
	if not header_end then
		http.status(500, "Internal Server Error")
		http.prepare_content("application/json")
		http.write_json({ error = "Invalid response from daemon" })
		return
	end
	
	local headers = response:sub(1, header_end - 1)
	local body = response:sub(header_end + 4)
	
	local status_code = 200
	local status_line = headers:match("HTTP/[%d%.]+%s+(%d+)")
	if status_line then
		status_code = tonumber(status_line)
	end
	
	http.status(status_code)
	http.prepare_content("application/json")
	http.write(body)
end
