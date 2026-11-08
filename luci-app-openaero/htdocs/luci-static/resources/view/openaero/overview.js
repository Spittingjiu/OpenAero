'use strict';
'require view';
'require fs';
'require ui';
return view.extend({
	handleSaveApply: null,
	handleSave: null,
	handleReset: null,
	activeTab: 'status',
	statusTimer: null,
	nodesCache: [],
	groupsCache: [],
	activeNodeId: '',
	load: function() {
		return Promise.all([
			this.apiGet('/api/status').catch(function() { return null; }),
			this.apiGet('/api/nodes').catch(function() { return []; }),
			this.apiGet('/api/config').catch(function() { return null; })
		]);
	},

	apiGet: function(endpoint) {
		var ep = (endpoint && endpoint.charAt(0) === '/') ? endpoint : '/' + (endpoint || '');
		var directUrl = '/openaero-api' + ep + (ep.indexOf('?') !== -1 ? '&' : '?') + '_t=' + Date.now();
		return fetch(directUrl, { cache: 'no-store' })
			.then(function(res) {
				if (!res.ok) throw new Error('HTTP ' + res.status);
				return res.json();
			})
			.catch(function() {
				var luaUrl = L.url('admin/services/openaero/proxy') + '?path=' + encodeURIComponent(endpoint) + '&_t=' + Date.now();
				return fetch(luaUrl, { cache: 'no-store' })
					.then(function(res) {
						if (!res.ok) throw new Error('HTTP ' + res.status);
						return res.json();
					});
			});
	},

	apiPost: function(endpoint, data) {
		var ep = (endpoint && endpoint.charAt(0) === '/') ? endpoint : '/' + (endpoint || '');
		var directUrl = '/openaero-api' + ep;
		return fetch(directUrl, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(data || {})
		}).then(function(res) {
			if (!res.ok) throw new Error('HTTP ' + res.status);
			return res.json();
		}).catch(function() {
			var luaUrl = L.url('admin/services/openaero/proxy') + '?path=' + encodeURIComponent(endpoint);
			return fetch(luaUrl, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(data || {})
			}).then(function(res) {
				if (!res.ok) throw new Error('HTTP ' + res.status);
				return res.json();
			});
		});
	},

	render: function(data) {
		var self = this;
		var status = data[0] || {};
		self.nodesCache = data[1] || [];
		var cfg = data[2] || {};
		self.groupsCache = cfg.strategy_groups || [];
		self.activeNodeId = status.active_node_id || cfg.active_node_id || (status.active_node && status.active_node.id) || '';

		// 1. Determine activeTab from L.env.dispatchpath or URL segment
		var pathSeg = (L.env && Array.isArray(L.env.dispatchpath) && L.env.dispatchpath.length >= 4)
			? L.env.dispatchpath[3]
			: (window.location.pathname.split('/').pop() || 'overview');
		if (pathSeg === 'nodes') self.activeTab = 'nodes';
		else if (pathSeg === 'rules') self.activeTab = 'rules';
		else if (pathSeg === 'settings') self.activeTab = 'settings';
		else self.activeTab = 'status';

		// Clean up any stale LuCI menu sessionStorage to ensure fresh top tabs
		try {
			for (var sk in sessionStorage) {
				if (sk && sk.indexOf('luci-menu') !== -1) {
					sessionStorage.removeItem(sk);
				}
			}
		} catch(e) {}

		var container = E('div', { 'class': 'openaero-wrap', 'style': 'max-width: 1200px; margin: 0 auto; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;' });

		// Responsive CSS Injection
		var responsiveStyle = E('style', {}, `
.openaero-wrap { max-width: 1200px; margin: 0 auto; box-sizing: border-box; }
.oa-header-card { background: linear-gradient(135deg, #1e3c72 0%, #2a5298 100%); color: #fff; padding: 22px 24px; border-radius: 12px; margin-bottom: 16px; box-shadow: 0 4px 15px rgba(0,0,0,0.1); display: flex; justify-content: space-between; align-items: center; gap: 16px; flex-wrap: wrap; }
.openaero-wrap .cbi-tabmenu, .openaero-wrap .oa-tab-nav { display: none !important; }
.openaero-wrap h2, .openaero-wrap h3 { background: transparent !important; box-shadow: none !important; }
.oa-stat-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; margin-bottom: 16px; }
.oa-group-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 16px; }
.oa-node-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 14px; }
.oa-ctrl-btn { min-height: 40px; display: inline-flex; align-items: center; justify-content: center; padding: 8px 16px; border-radius: 8px; font-weight: 600; cursor: pointer; border: none; touch-action: manipulation; }
.oa-select-touch { min-height: 42px; font-size: 14px; border-radius: 8px; border: 1px solid #cbd5e1; padding: 8px 12px; background-color: #fff; touch-action: manipulation; }
.oa-table-responsive { width: 100%; overflow-x: auto; -webkit-overflow-scrolling: touch; }
@media screen and (max-width: 768px) {
  .oa-header-card { padding: 16px !important; flex-direction: column !important; align-items: flex-start !important; }
  .oa-header-badge { width: 100% !important; justify-content: space-between !important; margin-top: 8px !important; }
  
  .oa-stat-grid { grid-template-columns: 1fr 1fr !important; gap: 10px !important; }
  .oa-group-grid { grid-template-columns: 1fr !important; gap: 12px !important; }
  .oa-node-grid { grid-template-columns: 1fr !important; gap: 10px !important; }
  .oa-form-grid { grid-template-columns: 1fr !important; gap: 14px !important; }
  .oa-action-row { flex-direction: column !important; gap: 8px !important; }
  .oa-action-row button { width: 100% !important; min-height: 44px !important; }
  .oa-select-touch { min-height: 44px !important; font-size: 14px !important; }
  .oa-log-pre { font-size: 11px !important; max-height: 220px !important; }
}
@media screen and (max-width: 480px) {
  .oa-stat-grid { grid-template-columns: 1fr !important; }
}`);
		container.appendChild(responsiveStyle);
		// Header Banner
		var header = E('div', { 'style': 'background: linear-gradient(135deg, #1e3c72 0%, #2a5298 100%); color: #fff; padding: 24px 28px; border-radius: 12px; margin-bottom: 20px; box-shadow: 0 4px 15px rgba(0,0,0,0.1); display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap;' }, [
			E('div', {}, [
				E('div', { 'style': 'color: #fff !important; background: transparent !important; padding: 0 !important; margin: 0 0 6px 0; font-size: 24px; font-weight: 700; letter-spacing: 0.5px; display: flex; align-items: center; gap: 10px;' }, [
					'🚀 OpenAero',
					E('span', { 'style': 'font-size: 13px; background: rgba(255,255,255,0.2); padding: 2px 10px; border-radius: 20px; font-weight: 400;' }, 'sing-box NextGen')
				]),
				E('div', { 'style': 'font-size: 13px; opacity: 0.85;' }, 'iStoreOS 专享极速分流代理中心 · Linux 原生 TUN · 策略组智能调度 · 零频 GC · 0-RTT 预热')
			]),
			E('div', { 'id': 'oa-header-badge', 'style': 'display: flex; gap: 10px; align-items: center; margin-top: 10px;' }, [
				self.renderCoreBadge(status)
			])
		]);
		container.appendChild(header);
		// Tab Panes (Consolidated 4 Unified Modules - Directly bound to iStoreOS native top #tabmenu)
		var paneStatus = E('div', { 'id': 'pane-status', 'style': 'display: ' + (self.activeTab === 'status' ? 'block' : 'none') + ';' }, self.renderTabStatus(status));
		var paneNodes = E('div', { 'id': 'pane-nodes', 'style': 'display: ' + (self.activeTab === 'nodes' ? 'block' : 'none') + ';' }, [
			self.renderTabGroups(self.groupsCache, self.nodesCache),
			E('div', { 'style': 'margin-top: 24px;' }, [
				self.renderTabNodes(self.nodesCache, status.active_node)
			])
		]);
		var paneRules = E('div', { 'id': 'pane-rules', 'style': 'display: ' + (self.activeTab === 'rules' ? 'block' : 'none') + ';' }, [
			self.renderTabAcl(cfg),
			E('div', { 'style': 'margin-top: 24px;' }, [
				self.renderTabRules(cfg)
			])
		]);
		var paneSettings = E('div', { 'id': 'pane-settings', 'style': 'display: ' + (self.activeTab === 'settings' ? 'block' : 'none') + ';' }, [
			self.renderTabSub(cfg),
			E('div', { 'style': 'margin-top: 24px;' }, [
				self.renderTabSettings(cfg)
			]),
			E('div', { 'style': 'margin-top: 24px;' }, [
				self.renderTabCore(status)
			])
		]);
		container.appendChild(paneStatus);
		container.appendChild(paneNodes);
		container.appendChild(paneRules);
		container.appendChild(paneSettings);

		// Mount hooks: bind native top tab menu and trigger initial fetches
		self.bindNativeTopTabMenu();
		setTimeout(function() {
			self.bindNativeTopTabMenu();
			self.fetchLogs();
			self.fetchAccessLogs();
			self.refreshStatus();
			self.checkBypassStatus();
		}, 50);
		setTimeout(function() {
			self.bindNativeTopTabMenu();
		}, 250);

		// Start background polling and fetch initial logs
		self.startPolling();
		return container;
	},

	switchTab: function(tabId) {
		this.activeTab = tabId;
		if (tabId !== 'status') {
			this.stopConnectionsPolling();
		}
		var panes = ['status', 'nodes', 'rules', 'settings'];
		panes.forEach(function(id) {
			var p = document.getElementById('pane-' + id);
			if (p) p.style.display = (id === tabId) ? 'block' : 'none';
		});

		// Update native top tabmenu active class in iStoreOS
		var tabsMap = {
			'status': 'overview',
			'nodes': 'nodes',
			'rules': 'rules',
			'settings': 'settings'
		};
		var activeName = tabsMap[tabId] || 'overview';
		var container = document.getElementById('tabmenu') || document.querySelector('#tabmenu');
		if (container) {
			var ul = container.querySelector('ul.tabs');
			if (ul) {
				var items = ul.querySelectorAll('li');
				for (var i = 0; i < items.length; i++) {
					var it = items[i];
					if (it.classList.contains('tabmenu-item-' + activeName) || it.className.indexOf(activeName) !== -1) {
						it.classList.add('active');
					} else {
						it.classList.remove('active');
					}
				}
			}
		}

		// Sync browser URL seamlessly
		var tabsPathMap = {
			'status': 'admin/services/openaero/overview',
			'nodes': 'admin/services/openaero/nodes',
			'rules': 'admin/services/openaero/rules',
			'settings': 'admin/services/openaero/settings'
		};
		if (tabsPathMap[tabId]) {
			try {
				window.history.replaceState(null, '', L.url(tabsPathMap[tabId]));
			} catch(e) {}
		}

		if (tabId === 'status') {
			this.fetchLogs();
		} else if (tabId === 'rules') {
			this.fetchLanDevices();
		} else if (tabId === 'nodes') {
			var needRefresh = false;
			if (this.nodesCache && this.nodesCache.length > 0) {
				needRefresh = this.nodesCache.some(function(n) {
					return !n.ortt_status || n.ortt_status === 'untested';
				});
			}
			if (needRefresh) {
				this.refreshStatus();
			}
		}
	},

	bindNativeTopTabMenu: function() {
		var self = this;
		var container = document.getElementById('tabmenu') || document.querySelector('#tabmenu');
		if (!container) return;

		var nativeTabs = [
			{ name: 'overview', tabId: 'status', label: '📊 运行概览', path: 'admin/services/openaero/overview' },
			{ name: 'nodes',    tabId: 'nodes',  label: '🌐 节点与分流', path: 'admin/services/openaero/nodes' },
			{ name: 'rules',    tabId: 'rules',  label: '🛡️ 规则与设备', path: 'admin/services/openaero/rules' },
			{ name: 'settings', tabId: 'settings', label: '⚙️ 系统与订阅', path: 'admin/services/openaero/settings' }
		];

		var ul = container.querySelector('ul.tabs');
		var items = ul ? ul.querySelectorAll('li') : [];

		// Reconstruct if ul is missing or has fewer than 4 items (e.g. stale sessionStorage cache)
		if (!ul || items.length < 4) {
			if (!ul) {
				ul = E('ul', { 'class': 'tabs' });
				container.appendChild(ul);
			} else {
				ul.innerHTML = '';
			}
			nativeTabs.forEach(function(t) {
				var isActive = (t.tabId === self.activeTab);
				var li = E('li', {
					'class': 'tabmenu-item-' + t.name + (isActive ? ' active' : '')
				}, [
					E('a', { 'href': L.url(t.path) }, [ t.label ])
				]);
			ul.appendChild(li);
			});
		}

		container.style.display = '';

		// Bind 0ms click interceptor to each li > a
		nativeTabs.forEach(function(t) {
			var li = ul.querySelector('li.tabmenu-item-' + t.name) ||
			         ul.querySelector('li[class*="' + t.name + '"]');
			if (!li) return;

			if (t.tabId === self.activeTab) {
				li.classList.add('active');
			} else {
				li.classList.remove('active');
			}

			var a = li.querySelector('a');
			if (a && !a._oa_bound) {
				a._oa_bound = true;
				a.onclick = function(ev) {
					if (ev) {
						ev.preventDefault();
						ev.stopPropagation();
					}
					self.switchTab(t.tabId);
					try {
						window.history.replaceState(null, '', L.url(t.path));
					} catch(e) {}
					return false;
				};
			}
		});
	},

	renderCoreBadge: function(status) {
		var isRunning = status && status.singbox_running;
		return E('div', {
			'style': 'display: flex; align-items: center; gap: 8px; background: ' + (isRunning ? 'rgba(16, 185, 129, 0.2)' : 'rgba(239, 68, 68, 0.2)') + '; border: 1px solid ' + (isRunning ? '#10b981' : '#ef4444') + '; padding: 6px 14px; border-radius: 30px;'
		}, [
			E('span', { 'style': 'width: 10px; height: 10px; border-radius: 50%; background: ' + (isRunning ? '#10b981' : '#ef4444') + '; display: inline-block;' }),
			E('span', { 'style': 'font-weight: 600; font-size: 13px; color: #fff;' }, isRunning ? '核心运行中' : '核心未启动')
		]);
	},

	renderTabStatus: function(status) {
		var self = this;
		var activeName = (status.active_node && status.active_node.remark) ? status.active_node.remark : '未指定';
		var activeLatency = (status.active_node && status.active_node.latency > 0) ? (status.active_node.latency + ' ms') : '未测速';
		var activeProto = (status.active_node && status.active_node.type) ? status.active_node.type.toUpperCase() : '-';
		var smTitle = '未启用';
		var smSub = '可在设置中开启';
		var actN = status.active_node;
		if ((status.ortt_mode || status.sing_matrix_mode) === 'disabled') {
			smTitle = '已停用';
			smSub = '可在全局设置开启';
		} else if (actN && (actN.ortt_warm_conns > 0 || actN.ortt_status === 'healthy')) {
			var hr = actN.ortt_hit_rate || '0%';
			smTitle = '🟢 热池: ' + (actN.ortt_warm_conns || 0) + ' · 命中率 ' + hr;
			var hits = (actN.ortt_pool_hits != null) ? actN.ortt_pool_hits : 0;
			var serverAddr = actN.server ? (actN.server + ':39999') : ((status.ortt_target || status.sing_matrix_target) || '39999');
			smSub = serverAddr + ' · 累计命中 ' + hits + ' 次';
		} else if ((status.ortt_mode || status.sing_matrix_mode) === 'follow_node') {
			var actRem = (actN && actN.remark) ? (' (' + actN.remark.replace(/^.*?- /,'') + ')') : '';
			smTitle = '跟随活跃节点' + actRem;
			smSub = (status.ortt_target || status.sing_matrix_target) || '端口: 39999';
		} else if ((status.ortt_mode || status.sing_matrix_mode) === 'custom') {
			smTitle = '自定义中继';
			smSub = (status.ortt_target || status.sing_matrix_target) || '自定义地址';
		}
		return [
			E('div', { 'class': 'oa-stat-grid', 'style': 'display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; margin-bottom: 24px;' }, [
				self.createCard('🌐 漏网之鱼默认出站', activeName, '延迟: ' + activeLatency + ' · ' + activeProto, '#3b82f6', 'oa-stat-node-val', 'oa-stat-node-sub'),
				self.createCard('⚡ 实时下行速率', '0 B/s', '总接收: 0 B', '#0284c7', 'oa-traffic-rx-val', 'oa-traffic-rx-sub'),
				self.createCard('⚡ 实时上行速率', '0 B/s', '总发送: 0 B', '#0d9488', 'oa-traffic-tx-val', 'oa-traffic-tx-sub'),
				self.createCard('🔗 活跃并发连接', '0 个', '实时活跃连接数', '#d97706', 'oa-traffic-conn-val', 'oa-traffic-conn-sub'),
				self.createCard('⚙️ 核心与进程', status.singbox_running ? ('PID: ' + status.singbox_pid) : '停止', '内存占用: ' + (status.singbox_mem || '0 MB'), '#10b981', 'oa-stat-proc-val', 'oa-stat-proc-sub'),
				self.createCard('🛡️ 防死锁看门狗', status.watchdog_status === 'running' ? '激活中' : '未启用', '心跳周期: 5s 自动断网自愈', '#8b5cf6', 'oa-stat-wd-val', 'oa-stat-wd-sub'),
				self.createCard('🚀 ORTT 极速预热', smTitle, smSub, '#f59e0b', 'oa-stat-sm-val', 'oa-stat-sm-sub')
			]),
			E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
				E('h3', { 'style': 'margin-top: 0; margin-bottom: 15px; font-size: 16px; font-weight: 600;' }, '⚡ 快速控制与启停'),
				E('div', { 'class': 'oa-action-row', 'style': 'display: flex; gap: 12px; flex-wrap: wrap;' }, [
					E('button', {
						'class': 'btn cbi-button-action oa-ctrl-btn',
						'style': 'padding: 8px 20px; font-weight: 600;',
						'click': function() { self.controlService('restart'); }
					}, '🔄 重启服务 (Reload)'),
					E('button', {
						'class': 'btn cbi-button-reset oa-ctrl-btn',
						'style': 'padding: 8px 20px; font-weight: 600; color: #ef4444;',
						'click': function() { self.controlService('stop'); }
					}, '⏹️ 停止服务'),
					E('button', {
						'class': 'btn cbi-button-apply oa-ctrl-btn',
						'style': 'padding: 8px 20px; font-weight: 600;',
						'click': function() { self.controlService('start'); }
					}, '▶️ 启动服务'),
					E('button', {
						'class': 'btn oa-ctrl-btn',
						'style': 'padding: 8px 20px; font-weight: 600;',
						'click': function() { self.refreshStatus(); self.fetchLogs(); }
					}, '🔃 刷新状态与日志')
				])
			]),
			// Bypass Gateway Mode & Anti-Blackhole Topology Card (Defect 4)
			E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
				E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; flex-wrap: wrap; gap: 10px;' }, [
					E('div', { 'style': 'display: flex; align-items: center; gap: 10px;' }, [
						E('h3', { 'style': 'margin: 0; font-size: 16px; font-weight: 700; color: #0f172a;' }, '🛡️ 旁路网关模式（Bypass Mode）与防断网拓扑诊断'),
						E('span', { 'id': 'oa-bypass-badge', 'style': 'background: #e2e8f0; color: #475569; padding: 2px 10px; border-radius: 12px; font-size: 11px; font-weight: 700;' }, '检测中...')
					]),
					E('div', { 'style': 'display: flex; gap: 8px;' }, [
						E('button', {
							'class': 'btn cbi-button-action btn-sm',
							'style': 'padding: 6px 14px; font-weight: 600;',
							'click': function() { self.toggleBypassMode(true); }
						}, '⚡ 一键接管局域网 DHCP 网关与 DNS'),
						E('button', {
							'class': 'btn btn-sm',
							'style': 'padding: 6px 14px; color: #64748b;',
							'click': function() { self.toggleBypassMode(false); }
						}, '↩️ 恢复默认 DHCP'),
						E('button', {
							'class': 'btn btn-sm',
							'style': 'padding: 6px 14px;',
							'click': function() { self.checkBypassStatus(); }
						}, '🔍 刷新拓扑')
					])
				]),
				E('div', { 'id': 'oa-bypass-info-box', 'style': 'background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 12px 16px; font-size: 13px; color: #334155; line-height: 1.6;' }, [
					E('div', { 'id': 'oa-bypass-status-text' }, '正在检测当前 DHCP Option 3 (网关) 与 Option 6 (DNS) 下发状态...'),
					E('div', { 'style': 'margin-top: 6px; font-size: 12px; color: #64748b;' }, '💡 提示：在单臂旁路由环境下，若终端仅将 DNS 指向本路由而默认网关仍指向主路由，因主路由不识别 198.18.0.0/15 将引发 Fake-IP 单边黑洞断网。一键接管将通过 DHCP Option 3 与 6 实现网关与 DNS 闭环，确保全家设备顺畅上网。')
				])
			]),
			// Real-Time Access Traffic Stream Card
			E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
				E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; flex-wrap: wrap; gap: 10px;' }, [
					E('div', { 'style': 'display: flex; align-items: center; gap: 10px;' }, [
						E('h3', { 'style': 'margin: 0; font-size: 16px; font-weight: 700; color: #0f172a;' }, '📡 实时流量分流访问日志 (Access Traffic Stream)'),
						E('span', { 'id': 'oa-access-log-badge', 'style': 'background: #dcfce7; color: #15803d; padding: 2px 10px; border-radius: 12px; font-size: 11px; font-weight: 700;' }, '● 实时监听中')
					]),
					E('div', { 'style': 'display: flex; gap: 8px;' }, [
						E('button', {
							'class': 'btn btn-sm',
							'style': 'padding: 4px 12px; font-weight: 600;',
							'click': function() { self.fetchAccessLogs(); }
						}, '🔄 刷新访问日志'),
						E('button', {
							'class': 'btn btn-sm',
							'style': 'padding: 4px 12px; color: #64748b;',
							'click': function() {
								var box = document.getElementById('oa-access-log-body');
								if (box) box.innerHTML = '<tr><td colspan="5" style="text-align: center; color: #94a3b8; padding: 24px;">已清空，等待新请求...</td></tr>';
							}
						}, '🗑️ 清屏')
					])
				]),
				E('div', { 'class': 'oa-table-responsive', 'style': 'overflow-x: auto; max-height: 280px; border: 1px solid #f1f5f9; border-radius: 8px;' }, [
					E('table', { 'class': 'table', 'style': 'width: 100%; min-width: 580px; border-collapse: collapse; font-size: 13px;' }, [
						E('thead', {}, [
							E('tr', { 'style': 'background: #f8fafc; color: #475569; position: sticky; top: 0; z-index: 2;' }, [
								E('th', { 'style': 'padding: 10px 14px; width: 90px;' }, '时间'),
								E('th', { 'style': 'padding: 10px 14px;' }, '访问目标 (Domain / IP)'),
								E('th', { 'style': 'padding: 10px 14px;' }, '出站链路与分流决策'),
								E('th', { 'style': 'padding: 10px 14px; width: 90px;' }, '耗时'),
								E('th', { 'style': 'padding: 10px 14px; width: 110px; text-align: center;' }, '动作')
							])
						]),
						E('tbody', { 'id': 'oa-access-log-body' }, [
							E('tr', {}, [
								E('td', { 'colspan': '5', 'style': 'text-align: center; color: #94a3b8; padding: 24px;' }, '正在监听实时网络请求（如访问 baidu.com 或 google.com 即可实时呈现分流记录）...')
							])
						])
					])
				])
			]),
			// Active Connections Real-Time Transparency (OA-SPEC-CONN-03)
			E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
				E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; flex-wrap: wrap; gap: 10px;' }, [
					E('div', { 'style': 'display: flex; align-items: center; gap: 10px;' }, [
						E('h3', { 'style': 'margin: 0; font-size: 16px; font-weight: 700; color: #0f172a;' }, '🔗 活跃网络连接实时透视 (Active Connections)'),
						E('span', { 'id': 'oa-conn-badge', 'style': 'background: #e0f2fe; color: #0369a1; padding: 2px 10px; border-radius: 12px; font-size: 11px; font-weight: 700;' }, 'Top 50 活跃流 (零频 GC 架构)')
					]),
					E('div', { 'style': 'display: flex; gap: 8px; align-items: center; flex-wrap: wrap;' }, [
						E('input', {
							'id': 'oa-conn-search',
							'type': 'text',
							'placeholder': '🔍 过滤 IP / 域名 / 规则...',
							'style': 'padding: 4px 10px; font-size: 12px; border: 1px solid #cbd5e1; border-radius: 6px; width: 170px;',
							'input': function(ev) { self.filterConnections(ev.target.value); }
						}),
						E('button', {
							'id': 'btn-toggle-conn',
							'class': 'btn btn-sm cbi-button-action',
							'style': 'padding: 4px 12px; font-weight: 600;',
							'click': function() { self.toggleConnectionsDrawer(); }
						}, '👁️ 展开实时透视'),
						E('button', {
							'class': 'btn btn-sm',
							'style': 'padding: 4px 12px; font-weight: 600;',
							'click': function() { self.fetchConnections(); }
						}, '🔄 刷新'),
						E('button', {
							'class': 'btn btn-sm cbi-button-reset',
							'style': 'padding: 4px 12px; color: #ef4444; font-weight: 600;',
							'click': function() { self.closeAllConnections(); }
						}, '💥 强断全部连接')
					])
				]),
				E('div', { 'id': 'oa-conn-drawer', 'style': 'display: none;' }, [
					E('div', { 'class': 'oa-table-responsive', 'style': 'overflow-x: auto; max-height: 380px; border: 1px solid #f1f5f9; border-radius: 8px;' }, [
						E('table', { 'class': 'table', 'style': 'width: 100%; min-width: 720px; border-collapse: collapse; font-size: 13px;' }, [
							E('thead', {}, [
								E('tr', { 'style': 'background: #f8fafc; color: #475569; position: sticky; top: 0; z-index: 2;' }, [
									E('th', { 'style': 'padding: 10px 14px; width: 140px;' }, '客户端设备'),
									E('th', { 'style': 'padding: 10px 14px;' }, '访问目标 (Domain / IP)'),
									E('th', { 'style': 'padding: 10px 14px; width: 130px;' }, '分流链路 / 规则'),
									E('th', { 'style': 'padding: 10px 14px; width: 110px;' }, '速率 (↓ / ↑)'),
									E('th', { 'style': 'padding: 10px 14px; width: 110px;' }, '累计传输'),
									E('th', { 'style': 'padding: 10px 14px; width: 80px;' }, '持续时间'),
									E('th', { 'style': 'padding: 10px 14px; width: 75px; text-align: center;' }, '操作')
								])
							]),
							E('tbody', { 'id': 'oa-conn-tbody' }, [
								E('tr', {}, [
									E('td', { 'colspan': '7', 'style': 'text-align: center; color: #94a3b8; padding: 24px;' }, '点击“展开实时透视”开启动态连接监控...')
								])
							])
						])
					])
				])
			]),
			E('div', { 'class': 'cbi-section', 'style': 'background: #1e1e1e; color: #d4d4d4; padding: 16px 20px; border-radius: 12px; font-family: Consolas, monospace; font-size: 12px;' }, [
				E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; border-bottom: 1px solid #333; padding-bottom: 8px;' }, [
					E('div', { 'style': 'display: flex; align-items: center; gap: 10px;' }, [
						E('span', { 'style': 'font-weight: 600; color: #60a5fa;' }, '📋 内核实时日志 (Live Stream)'),
						E('span', { 'id': 'oa-log-status', 'style': 'font-size: 11px; color: #94a3b8;' }, '🟢 实时读取中...')
					]),
					E('button', {
						'class': 'btn btn-sm',
						'style': 'background: #333; color: #fff; border: none; padding: 2px 10px; border-radius: 4px; cursor: pointer;',
						'click': function() { self.fetchLogs(); }
					}, '刷新日志')
				]),
				E('pre', { 'id': 'oa-log-box', 'class': 'oa-log-pre', 'style': 'margin: 0; max-height: 280px; overflow-y: auto; white-space: pre-wrap; word-break: break-all; color: #a3e635; line-height: 1.5;' }, '正在连接系统实时日志流 (如未显示可点击刷新日志)...')
			])
		];
	},

	formatGroupTargetLabel: function(grp, nodes) {
		var self = this;
		var targetId = (typeof grp === 'string') ? grp : (grp && grp.target_node_id);
		nodes = nodes || self.nodesCache || [];
		if (targetId === 'DIRECT') return '🇨🇳 DIRECT 国内直连';
		if (targetId === 'REJECT' || targetId === 'BLOCK') return '🛑 REJECT 拦截阻断';
		if (targetId === 'auto_select') return '⚡ 自动测速优选 (URL-Test)';
		if (targetId && targetId !== 'GLOBAL') {
			var tn = nodes.find(function(n) { return n.id === targetId; });
			if (tn) return self.getFlagEmoji(tn.remark, tn.server) + ' ' + tn.remark;
		}
		if (self.activeNodeId === 'auto_select') {
			return '🌟 跟随主节点: ⚡ 自动测速优选 (URL-Test)';
		}
		var mainNode = nodes.find(function(n) { return n.id === self.activeNodeId; });
		if (mainNode) {
			return '🌟 跟随主节点: ' + self.getFlagEmoji(mainNode.remark, mainNode.server) + ' ' + mainNode.remark;
		}
		return '🌟 默认跟随主节点 (GLOBAL)';
	},

	renderTabGroups: function(groups, nodes) {
		var self = this;
		var wrap = E('div', {});

		var topNote = E('div', {
			'style': 'background: #eff6ff; border-left: 4px solid #3b82f6; padding: 14px 18px; border-radius: 8px; margin-bottom: 20px; font-size: 13px; color: #1e40af; line-height: 1.6;'
		}, [
			E('div', { 'style': 'font-weight: 700; margin-bottom: 4px;' }, '💡 策略组分流调度中心'),
			E('div', {}, '针对 Gemini、ChatGPT、YouTube、Telegram、Google 等业务流独立绑定最优出站节点，切换实时生效并通过金丝雀健康校验！')
		]);
		wrap.appendChild(topNote);
		var grid = E('div', { 'class': 'oa-group-grid' });

		var descMap = {
			'gemini': '针对 Google Gemini、AI Studio 及 DeepMind 算力业务',
			'chatgpt': '针对 OpenAI ChatGPT、API 接口及认证全域加速',
			'youtube': '针对 YouTube 视频流、音频媒体及 CDN 节点专属出站',
			'telegram': '针对 Telegram 聊天消息、媒体文件及官方 DC 直通',
			'google': '针对 Google 搜索、Play 商店及 Google 基础服务',
			'match': '未命中其它策略组的所有境外未知兜底流量'
		};
		groups.forEach(function(grp) {
			if (grp.id === 'proxy') return; // 移除废弃全局主代理组
			var desc = descMap[grp.id] || (grp.name + ' 专属出站调度策略');
			var targetLabel = self.formatGroupTargetLabel(grp, nodes);
			var card = E('div', {
				'class': 'cbi-section oa-card-pad',
				'style': 'background: #fff; border: 1px solid #e2e8f0; border-radius: 12px; padding: 20px; box-shadow: 0 2px 8px rgba(0,0,0,0.03); display: flex; flex-direction: column; justify-content: space-between;'
			});

			// Header
			var header = E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;' }, [
				E('div', { 'style': 'display: flex; align-items: center; gap: 8px; font-size: 16px; font-weight: 700; color: #1e293b;' }, [
					E('span', { 'style': 'font-size: 22px;' }, grp.icon || '🌐'),
					grp.name
				]),
				E('span', { 'style': 'font-size: 11px; background: #f1f5f9; color: #64748b; padding: 2px 8px; border-radius: 10px; font-weight: 600;' }, grp.id)
			]);
			card.appendChild(header);
			// Description
			var descEl = E('div', { 'style': 'font-size: 12px; color: #64748b; margin-bottom: 14px; min-height: 18px;' }, desc);
			card.appendChild(descEl);
			// Current active route tag
			var statusTagSpan = E('span', {
				'id': 'oa-grp-target-' + grp.id,
				'style': 'font-weight: 600; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;'
			}, targetLabel);
			var statusTag = E('div', {
				'style': 'font-size: 12px; background: #f8fafc; border: 1px solid #e2e8f0; padding: 6px 12px; border-radius: 8px; color: #334155; margin-bottom: 12px; display: flex; align-items: center; justify-content: space-between;'
			}, [
				E('span', { 'style': 'color: #94a3b8;' }, '当前出站:'),
				statusTagSpan
			]);
			card.appendChild(statusTag);
			// Selector
			var select = E('select', {
				'class': 'oa-select-touch',
				'style': 'width: 100%; min-height: 42px; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1; font-size: 14px; background: #fff;'
			});

			if (grp.id === 'match') {
				var optAuto = E('option', { 'value': 'auto_select' }, '⚡ 自动测速优选 (URL-Test 最低延时)');
				if (grp.target_node_id === 'auto_select' || (!grp.target_node_id && self.activeNodeId === 'auto_select')) optAuto.selected = true;
				select.appendChild(optAuto);
				nodes.forEach(function(n) {
					var flag = self.getFlagEmoji(n.remark, n.server);
					var opt = E('option', { 'value': n.id }, flag + ' ' + n.remark + ' (' + n.type.toUpperCase() + (n.latency > 0 ? (' · ' + n.latency + 'ms') : '') + ')');
					if (grp.target_node_id === n.id || (!grp.target_node_id && self.activeNodeId === n.id)) opt.selected = true;
					select.appendChild(opt);
				});
				var optDirect = E('option', { 'value': 'DIRECT' }, '🇨🇳 DIRECT (全直连兜底)');
				if (grp.target_node_id === 'DIRECT') optDirect.selected = true;
				select.appendChild(optDirect);
			} else {
				var optDefault = E('option', { 'value': 'GLOBAL' }, '🌟 跟随全局主节点 (GLOBAL)');
				if (!grp.target_node_id || grp.target_node_id === 'GLOBAL') optDefault.selected = true;
				select.appendChild(optDefault);
				var optAuto = E('option', { 'value': 'auto_select' }, '⚡ 自动测速优选 (URL-Test 最低延时)');
				if (grp.target_node_id === 'auto_select') optAuto.selected = true;
				select.appendChild(optAuto);
				var optDirect = E('option', { 'value': 'DIRECT' }, '🇨🇳 DIRECT (国内直连)');
				if (grp.target_node_id === 'DIRECT') optDirect.selected = true;
				select.appendChild(optDirect);
				var optReject = E('option', { 'value': 'REJECT' }, '🛑 REJECT (拦截阻断)');
				if (grp.target_node_id === 'REJECT') optReject.selected = true;
				select.appendChild(optReject);
				nodes.forEach(function(n) {
					var flag = self.getFlagEmoji(n.remark, n.server);
					var opt = E('option', { 'value': n.id }, '⚡ ' + flag + ' ' + n.remark + ' (' + n.type.toUpperCase() + (n.latency > 0 ? (' · ' + n.latency + 'ms') : '') + ')');
					if (grp.target_node_id === n.id) opt.selected = true;
					select.appendChild(opt);
				});
			}
			select.onchange = function() {
				var targetId = select.value;
				ui.showIndicator();
				self.apiPost('/api/groups/set', { id: grp.id, target_node_id: targetId })
					.then(function(res) {
						ui.hideIndicator();
						if (res && res.rolled_back) {
							ui.addNotification(null, E('p', {}, '⚠️ ' + (res.error || '策略组节点不可达，已自动回退！')), 'warning');
						} else {
							grp.target_node_id = targetId;
							if (grp.id === 'match' && targetId && targetId !== 'DIRECT' && targetId !== 'REJECT') {
								self.activeNodeId = targetId;
							}
							var newLabel = self.formatGroupTargetLabel(targetId, self.nodesCache || nodes);
							if (statusTagSpan) statusTagSpan.textContent = newLabel;
							var timeStr = (res && res.time_ms !== undefined) ? (' (' + res.time_ms + 'ms)') : '';
							ui.addNotification(null, E('p', {}, '✅ 策略组 [' + grp.name + '] 已秒级切换出站节点' + timeStr + '！'), 'success');
							self.refreshStatus();
							self.refreshNodes();
						}
					})
					.catch(function(err) {
						ui.hideIndicator();
						ui.addNotification(null, E('p', {}, '❌ 设置策略组失败: ' + err.message), 'error');
					});
			};
			card.appendChild(select);
			grid.appendChild(card);
		});

		wrap.appendChild(grid);
		wrap.appendChild(self.renderRouteDiagnosis());
		return wrap;
	},

	renderRouteDiagnosis: function() {
		var self = this;
		var card = E('div', {
			'class': 'cbi-section',
			'style': 'background: #fff; padding: 22px; border-radius: 12px; border: 1px solid #e2e8f0; margin-top: 24px;'
		});

		var titleRow = E('div', { 'style': 'margin-bottom: 16px;' }, [
			E('h3', { 'style': 'margin: 0 0 6px 0; font-size: 16px; font-weight: 700; color: #0f172a;' }, '🧭 一键分流路径透视与路由自愈诊断 (Route Pipeline Diagnosis)'),
			E('div', { 'style': 'font-size: 13px; color: #64748b;' }, '输入目标域名或 IP，模拟规则树自顶向下匹配，透视其 DNS 解析结果、命中的分流规则集、出站策略组及真实节点握手延时')
		]);
		card.appendChild(titleRow);
		var inputRow = E('div', { 'style': 'display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap;' }, [
			E('input', {
				'id': 'oa-diag-input',
				'type': 'text',
				'placeholder': '例如: www.google.com, chatgpt.com, www.bilibili.com, 1.1.1.1',
				'style': 'flex: 1; min-width: 260px; padding: 10px 14px; border-radius: 8px; border: 1px solid #cbd5e1; font-size: 14px; font-family: monospace;'
			}),
			E('button', {
				'id': 'btn-diag-run',
				'class': 'btn cbi-button-action',
				'style': 'padding: 10px 24px; font-weight: 600; font-size: 14px; white-space: nowrap;',
				'click': function() { self.runRouteDiagnosis(); }
			}, '🚀 开始链路透视诊断')
		]);
		card.appendChild(inputRow);
		var resultBox = E('div', {
			'id': 'oa-diag-result',
			'style': 'display: none; background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 16px;'
		});
		card.appendChild(resultBox);
		return card;
	},

	runRouteDiagnosis: function() {
		var self = this;
		var inp = document.getElementById('oa-diag-input');
		var btn = document.getElementById('btn-diag-run');
		var resBox = document.getElementById('oa-diag-result');
		if (!inp || !inp.value.trim()) {
			ui.addNotification(null, E('p', {}, '请输入需要透视诊断的目标域名或 IP！'), 'warning');
			return;
		}
		var target = inp.value.trim();
		if (btn) btn.innerText = '⏳ 正在透视链路中...';
		if (resBox) {
			resBox.style.display = 'block';
			resBox.innerHTML = '<div style="color: #64748b; font-size: 13px;">正在解析 DNS 并模拟规则树匹配...</div>';
		}
		self.apiPost('/api/diagnose_route', { target: target })
			.then(function(res) {
				if (btn) btn.innerText = '🚀 开始链路透视诊断';
				if (!resBox) return;
				resBox.innerHTML = '';
				var statusColor = '#10b981';
				if (res.status && (res.status.indexOf('超时') !== -1 || res.status.indexOf('失败') !== -1)) {
					statusColor = '#ef4444';
				} else if (res.outbound_type === 'block') {
					statusColor = '#f59e0b';
				}
				// Top badges
				var headerRow = E('div', { 'style': 'display: flex; gap: 10px; margin-bottom: 12px; flex-wrap: wrap; align-items: center;' }, [
					E('span', { 'style': 'font-weight: 700; font-size: 14px; color: #1e293b;' }, '诊断目标: ' + res.target),
					E('span', { 'style': 'background: #e2e8f0; color: #334155; padding: 2px 8px; border-radius: 6px; font-size: 12px; font-family: monospace;' }, 'IP: ' + res.resolved_ip),
					E('span', { 'style': 'background: ' + statusColor + '20; color: ' + statusColor + '; padding: 2px 10px; border-radius: 6px; font-size: 12px; font-weight: 700;' }, (res.latency_ms > 0 ? (res.latency_ms + ' ms · ') : '') + res.status)
				]);
				resBox.appendChild(headerRow);
				// Pipeline trace
				var traceList = E('div', { 'style': 'display: flex; flex-direction: column; gap: 8px;' });
				(res.pipeline_trace || []).forEach(function(step, idx) {
					var item = E('div', { 'style': 'display: flex; align-items: center; gap: 10px; font-size: 13px; color: #334155;' }, [
						E('span', { 'style': 'width: 22px; height: 22px; border-radius: 50%; background: #3b82f6; color: #fff; display: inline-flex; align-items: center; justify-content: center; font-size: 11px; font-weight: 700; flex-shrink: 0;' }, '' + (idx + 1)),
						E('span', { 'style': 'flex: 1;' }, step)
					]);
					traceList.appendChild(item);
				});
				resBox.appendChild(traceList);
			})
			.catch(function(err) {
				if (btn) btn.innerText = '🚀 开始链路透视诊断';
				if (resBox) {
					resBox.style.display = 'block';
					resBox.innerHTML = '<div style="color: #ef4444; font-size: 13px;">❌ 诊断失败: ' + err.message + '</div>';
				}
			});
	},

	renderTabNodes: function(nodes, activeNode) {
		var self = this;
		var wrap = E('div', {});
		self.currentFilterRegion = 'all';
		self.currentSearchKeyword = '';
		var topBar = E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; flex-wrap: wrap; gap: 12px;' }, [
			E('div', { 'style': 'display: flex; align-items: center; gap: 12px;' }, [
				E('span', { 'style': 'font-size: 16px; font-weight: 700; color: #0f172a;' }, '🌐 节点池管理 (' + nodes.length + ' 个)'),
				E('input', {
					'id': 'oa-node-search',
					'type': 'text',
					'placeholder': '🔍 搜索节点名/IP/协议...',
					'style': 'padding: 6px 12px; border-radius: 8px; border: 1px solid #cbd5e1; font-size: 13px; width: 180px;',
					'input': function(ev) {
						self.currentSearchKeyword = ev.target.value.trim();
						self.renderNodeCards(self.nodesCache || nodes, activeNode, self.currentFilterRegion, self.currentSearchKeyword);
					}
				})
			]),
			E('div', { 'style': 'display: flex; gap: 8px; flex-wrap: wrap;' }, [
				E('button', {
					'class': 'btn cbi-button-action',
					'style': 'padding: 6px 16px; font-weight: 600;',
					'click': function() { self.benchmarkAllNodes(); }
				}, '⚡ 一键全节点测速'),
				E('button', {
					'class': 'btn cbi-button-action',
					'style': 'padding: 6px 16px; font-weight: 600; background: linear-gradient(135deg, #7c3aed 0%, #4f46e5 100%); border-color: #7c3aed; color: #fff;',
					'click': function() { self.selectFastestHealthyNode(); }
				}, '🏆 优选最低延迟节点'),
				E('button', {
					'class': 'btn cbi-button-action',
					'style': 'padding: 6px 16px; font-weight: 600; background: #059669; border-color: #059669;',
					'click': function() { self.testAllNodesOrtt(); }
				}, '🔍 全链路检测 ORTT'),
				E('button', {
					'class': 'btn',
					'style': 'padding: 6px 16px;',
					'click': function() { self.refreshNodes(); }
				}, '🔄 刷新节点')
			])
		]);
		wrap.appendChild(topBar);
		var orttDesc = E('div', { 'style': 'background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 8px 12px; margin-bottom: 14px; font-size: 12px; color: #64748b; display: flex; align-items: center; gap: 6px;' }, [
			E('span', { 'style': 'font-weight: 700; color: #0f172a;' }, 'ℹ️ 全链路 ORTT 检测：'),
			'实测 1. UDP 39999 HMAC 双向鉴权 ➔ 2. 远端 39998 数据面热池挂载 ➔ 3. 真实 HTTPS 穿透命中计数，失效如实报告。'
		]);
		wrap.appendChild(orttDesc);
		// Region Filter Pills
		var filterPills = [
			{ id: 'all', name: '全部节点 (' + nodes.length + ')' },
			{ id: 'hk', name: '🇭🇰 香港' },
			{ id: 'jp', name: '🇯🇵 日本' },
			{ id: 'us', name: '🇺🇸 美国' },
			{ id: 'de', name: '🇩🇪 欧洲' },
			{ id: 'sg', name: '🇸🇬 新加坡' }
		];
		var pillsRow = E('div', { 'style': 'display: flex; gap: 8px; margin-bottom: 16px; flex-wrap: wrap;' }, filterPills.map(function(pill) {
			return E('button', {
				'class': 'oa-filter-pill',
				'id': 'pill-node-' + pill.id,
				'style': 'padding: 4px 14px; border-radius: 20px; border: 1px solid #cbd5e1; background: ' + (pill.id === 'all' ? '#0f172a' : '#fff') + '; color: ' + (pill.id === 'all' ? '#fff' : '#475569') + '; font-size: 12px; font-weight: 600; cursor: pointer; transition: all 0.2s;',
				'click': function() {
					self.currentFilterRegion = pill.id;
					filterPills.forEach(function(p) {
						var el = document.getElementById('pill-node-' + p.id);
						if (el) {
							el.style.background = (p.id === pill.id ? '#0f172a' : '#fff');
							el.style.color = (p.id === pill.id ? '#fff' : '#475569');
							el.style.borderColor = (p.id === pill.id ? '#0f172a' : '#cbd5e1');
						}
					});
					self.renderNodeCards(self.nodesCache || nodes, activeNode, self.currentFilterRegion, self.currentSearchKeyword);
				}
			}, pill.name);
		}));
		wrap.appendChild(pillsRow);
		// Card Grid container
		var grid = E('div', {
			'id': 'oa-nodes-grid',
			'style': 'display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px;'
		});
		wrap.appendChild(grid);
		setTimeout(function() {
			self.renderNodeCards(nodes, activeNode, self.currentFilterRegion, self.currentSearchKeyword);
		}, 10);
		return wrap;
	},

	renderNodeCards: function(nodes, activeNode, filterRegion, searchKeyword) {
		var self = this;
		var container = document.getElementById('oa-nodes-grid');
		if (!container) return;
		container.innerHTML = '';
		var filtered = nodes.filter(function(n) {
			if (filterRegion && filterRegion !== 'all') {
				var flag = self.getFlagEmoji(n.remark, n.server);
				if (filterRegion === 'hk' && flag !== '🇭🇰') return false;
				if (filterRegion === 'jp' && flag !== '🇯🇵') return false;
				if (filterRegion === 'us' && flag !== '🇺🇸') return false;
				if (filterRegion === 'de' && flag !== '🇩🇪') return false;
				if (filterRegion === 'sg' && flag !== '🇸🇬') return false;
			}
			if (searchKeyword) {
				var kw = searchKeyword.toLowerCase();
				if ((n.remark || '').toLowerCase().indexOf(kw) === -1 &&
				    (n.server || '').toLowerCase().indexOf(kw) === -1 &&
				    (n.type || '').toLowerCase().indexOf(kw) === -1) {
					return false;
				}
			}
			return true;
		});

		if (filtered.length === 0) {
			container.appendChild(E('div', {
				'style': 'grid-column: 1 / -1; text-align: center; color: #94a3b8; padding: 40px; background: #fff; border-radius: 12px; border: 1px dashed #cbd5e1;'
			}, '未找到匹配的节点'));
			return;
		}

		var minLat = 999999;
		var fastestNodeId = null;
		nodes.forEach(function(n) {
			if (n.latency > 0 && n.latency < minLat) {
				var r = (n.remark || '').toLowerCase();
				if (r.indexOf('剩余流量') === -1 && r.indexOf('到期时间') === -1 && r.indexOf('套餐') === -1 && r.indexOf('官网') === -1) {
					minLat = n.latency;
					fastestNodeId = n.id;
				}
			}
		});

		filtered.forEach(function(n) {
			var isActive = activeNode && (activeNode.id === n.id);
			var flag = self.getFlagEmoji(n.remark, n.server);
			var protoColor = '#3b82f6';
			if (n.type === 'hysteria2') protoColor = '#8b5cf6';
			else if (n.type === 'anytls') protoColor = '#f59e0b';
			else if (n.type === 'shadowsocks') protoColor = '#10b981';
			var latText = (n.latency > 0) ? (n.latency + ' ms') : (n.latency === -1 ? '超时' : '未测');
			var latBg = '#f1f5f9', latFg = '#64748b';
			if (n.latency > 0 && n.latency < 120) {
				latBg = '#dcfce7'; latFg = '#15803d';
			} else if (n.latency >= 120 && n.latency < 250) {
				latBg = '#fef3c7'; latFg = '#b45309';
			} else if (n.latency >= 250) {
				latBg = '#fee2e2'; latFg = '#b91c1c';
			}
			var card = E('div', {
				'class': 'oa-node-card',
				'style': 'background: ' + (isActive ? '#f0fdf4' : '#ffffff') + ';' +
				         'border: ' + (isActive ? '2px solid #10b981' : '1px solid #e2e8f0') + ';' +
				         'border-radius: 12px; padding: 14px 16px; cursor: pointer; position: relative;' +
				         'transition: all 0.2s ease; box-shadow: ' + (isActive ? '0 4px 12px rgba(16,185,129,0.15)' : '0 1px 3px rgba(0,0,0,0.04)') + ';',
				'click': function(ev) {
					self.selectActiveNode(n.id);
				}
			}, [
				E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;' }, [
					E('div', { 'style': 'font-weight: 700; font-size: 14px; color: #1e293b; display: flex; align-items: center; gap: 6px;' }, [
						E('span', { 'style': 'font-size: 18px;' }, flag),
						E('span', { 'title': n.remark, 'style': 'max-width: 125px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;' }, n.remark),
						(n.id === fastestNodeId) ? E('span', {
							'style': 'background: linear-gradient(135deg, #8b5cf6 0%, #ec4899 100%); color: #fff; padding: 2px 6px; border-radius: 4px; font-size: 10px; font-weight: 800; letter-spacing: 0.5px; box-shadow: 0 1px 4px rgba(139,92,246,0.3);'
						}, '⚡ 最快') : E('span', {}, '')
					]),
					E('span', { 'style': 'background: ' + protoColor + '18; color: ' + protoColor + '; padding: 2px 8px; border-radius: 6px; font-size: 11px; font-weight: 700;' }, n.type.toUpperCase())
				]),
				E('div', { 'style': 'font-family: monospace; font-size: 11px; color: #64748b; margin-bottom: 8px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;' }, n.server + ':' + n.port),
				(function() {
					var orttBg = '#f1f5f9', orttFg = '#64748b', orttBorder = '#cbd5e1';
					var orttText = n.ortt_label || '⚪ ORTT 待检测 (点击检测)';
					if (n.ortt_status === 'healthy') {
						orttBg = '#ecfdf5'; orttFg = '#047857'; orttBorder = '#a7f3d0';
					} else if (n.ortt_status === 'broken') {
						orttBg = '#fef2f2'; orttFg = '#b91c1c'; orttBorder = '#fecaca';
					} else if (n.ortt_status === 'degraded') {
						orttBg = '#fffbeb'; orttFg = '#b45309'; orttBorder = '#fde68a';
					}
					return E('div', {
						'class': 'oa-ortt-badge',
						'style': 'display: flex; justify-content: space-between; align-items: center; background: ' + orttBg + '; color: ' + orttFg + '; border: 1px solid ' + orttBorder + '; border-radius: 6px; padding: 4px 8px; font-size: 11px; font-weight: 600; margin-bottom: 10px; cursor: pointer;',
						'title': '点击单独检测该节点的 ORTT 全链路状态',
						'click': function(ev) {
							ev.stopPropagation();
							self.testSingleNodeOrtt(n.id);
						}
					}, [
						E('span', { 'style': 'overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 200px;' }, orttText),
						(n.ortt_rtt_ms > 0) ? E('span', { 'style': 'font-size: 10px; opacity: 0.85; margin-left: 6px;' }, n.ortt_rtt_ms + 'ms') : E('span', {}, '')
					]);
				})(),
				E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center;' }, [
					E('span', { 'style': 'background: ' + latBg + '; color: ' + latFg + '; padding: 3px 10px; border-radius: 20px; font-size: 12px; font-weight: 700;' }, latText),
					isActive ?
						E('span', { 'style': 'font-size: 12px; color: #10b981; font-weight: 700; display: flex; align-items: center; gap: 4px;' }, ['● 漏网之鱼生效中']) :
						E('span', { 'style': 'font-size: 12px; color: #3b82f6; font-weight: 600;' }, '设为默认出站 ➔')
				])
			]);
			container.appendChild(card);
		});
	},

	renderTabAcl: function(cfg) {
		var self = this;
		var wrap = E('div', {});
		var acl = (cfg && cfg.access_control) ? cfg.access_control : { enabled: true, mode: 'whitelist', bypass_ips: [], bypass_macs: [] };
		var bypassList = (acl.bypass_ips || []).join('\n');
		var bypassMacList = (acl.bypass_macs || []).join('\n');
		// 1. Header Banner & Global ACL Toggle
		var topCard = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 22px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
			E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 12px; margin-bottom: 12px;' }, [
				E('div', {}, [
					E('h3', { 'style': 'margin: 0 0 6px 0; font-size: 18px; font-weight: 700; color: #0f172a;' }, '🛡️ 局域网设备访问控制 (Access Control List)'),
					E('div', { 'style': 'font-size: 13px; color: #64748b;' }, '针对智能电视 (Sony/小米/Apple TV)、电视盒子、IoT 智能家居配置「直连免代理名单」，支持 IP/CIDR 与硬件 MAC 物理级旁路。')
				]),
				E('div', { 'style': 'display: flex; gap: 12px; align-items: center;' }, [
					E('label', { 'style': 'display: flex; align-items: center; gap: 8px; font-weight: 600; cursor: pointer;' }, [
						E('input', {
							'id': 'oa-acl-enabled-switch',
							'type': 'checkbox',
							'checked': (acl.enabled !== false),
							'style': 'transform: scale(1.2); cursor: pointer;'
						}),
						E('span', { 'style': 'font-size: 14px;' }, '启用设备访问控制')
					]),
					E('button', {
						'class': 'btn cbi-button-apply',
						'style': 'padding: 6px 18px; font-weight: 600;',
						'click': function() { self.saveAclSettings(); }
					}, '💾 保存并应用')
				])
			]),
			E('div', { 'style': 'background: #f8fafc; border-left: 4px solid #10b981; padding: 12px 16px; border-radius: 6px; font-size: 13px; color: #065f46;' }, [
				E('span', { 'style': 'font-weight: 700;' }, '⚡ 双轨四重硬核穿透机制：'),
				' IP/MAC 绕过规则自动持久化写入 /usr/share/nftables.d/，防火墙重载 (Reload) 永不丢失；内核路由优先表 (Priority 8990) + L2/L3 ether saddr 硬件免劫持 + sing-box Priority 0 直通出网！'
			])
		]);
		wrap.appendChild(topCard);
		// 2. LAN Online Devices (DHCP / ARP)
		var devCard = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 22px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
			E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; flex-wrap: wrap; gap: 10px;' }, [
				E('div', {}, [
					E('h3', { 'style': 'margin: 0 0 4px 0; font-size: 16px; font-weight: 700; color: #0f172a;' }, '📱 局域网在线设备一键分流管控 (DHCP / ARP 在线列表)'),
					E('div', { 'style': 'font-size: 12px; color: #64748b;' }, '实时抓取路由器 DHCP 租约与 ARP 活跃表，点击即可一键锁定 IP/MAC 双免代理')
				]),
				E('button', {
					'class': 'btn btn-sm',
					'style': 'padding: 6px 14px; font-weight: 600;',
					'click': function() { self.fetchLanDevices(); }
				}, '🔄 刷新在线设备')
			]),
			E('div', { 'class': 'oa-table-responsive', 'style': 'overflow-x: auto; max-height: 340px; border: 1px solid #f1f5f9; border-radius: 8px;' }, [
				E('table', { 'class': 'table', 'style': 'width: 100%; min-width: 650px; border-collapse: collapse; font-size: 13px;' }, [
					E('thead', {}, [
						E('tr', { 'style': 'background: #f8fafc; color: #475569; position: sticky; top: 0; z-index: 2;' }, [
							E('th', { 'style': 'padding: 10px 14px;' }, '设备标识 / 主机名'),
							E('th', { 'style': 'padding: 10px 14px; width: 140px;' }, 'IP 地址'),
							E('th', { 'style': 'padding: 10px 14px; width: 160px;' }, 'MAC 硬件地址'),
							E('th', { 'style': 'padding: 10px 14px; width: 140px;' }, '当前分流策略'),
							E('th', { 'style': 'padding: 10px 14px; width: 130px; text-align: center;' }, '一键策略切换')
						])
					]),
					E('tbody', { 'id': 'oa-lan-devices-tbody' }, [
						E('tr', {}, [
							E('td', { 'colspan': '5', 'style': 'text-align: center; color: #94a3b8; padding: 24px;' }, '正在扫描局域网在线设备...')
						])
					])
				])
			])
		]);
		wrap.appendChild(devCard);
		// 3. Manual IP / MAC Lists & Diagnostic Trace Tool
		var botGrid = E('div', { 'style': 'display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 20px;' }, [
			// Manual Bypass IP List
			E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0;' }, [
				E('h3', { 'style': 'margin-top: 0; margin-bottom: 6px; font-size: 16px; font-weight: 700;' }, '✏️ 直连免代理 IP / CIDR 清单'),
				E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 12px;' }, '支持单 IP（如 192.168.1.150）或整个网段 CIDR，每行一个。'),
				E('textarea', {
					'id': 'oa-acl-manual-ips',
					'rows': '6',
					'style': 'width: 100%; border-radius: 8px; border: 1px solid #cbd5e1; padding: 10px; font-family: monospace; font-size: 13px; margin-bottom: 12px;'
				}, bypassList),
				E('button', {
					'class': 'btn cbi-button-apply',
					'style': 'padding: 6px 18px; font-weight: 600;',
					'click': function() { self.saveAclSettings(); }
				}, '💾 保存并热生效')
			]),
			// Manual Bypass MAC List (Defect 1)
			E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0;' }, [
				E('h3', { 'style': 'margin-top: 0; margin-bottom: 6px; font-size: 16px; font-weight: 700;' }, '🏷️ 直连免代理 MAC 地址清单'),
				E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 12px;' }, '支持硬件 MAC 地址（如 aa:bb:cc:dd:ee:01），换 IP 依然免代理直通。'),
				E('textarea', {
					'id': 'oa-acl-manual-macs',
					'rows': '6',
					'style': 'width: 100%; border-radius: 8px; border: 1px solid #cbd5e1; padding: 10px; font-family: monospace; font-size: 13px; margin-bottom: 12px;'
				}, bypassMacList),
				E('button', {
					'class': 'btn cbi-button-apply',
					'style': 'padding: 6px 18px; font-weight: 600;',
					'click': function() { self.saveAclSettings(); }
				}, '💾 保存 MAC 名单')
			]),
			// ACL Client Route Trace
			E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0;' }, [
				E('h3', { 'style': 'margin-top: 0; margin-bottom: 6px; font-size: 16px; font-weight: 700;' }, '🔍 终端设备分流路由透视诊断'),
				E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 12px;' }, '输入特定客户端 IP 与访问目标，实测其是否命中直连白名单或分流规则。'),
				E('div', { 'style': 'display: flex; gap: 8px; margin-bottom: 10px;' }, [
					E('input', {
						'id': 'oa-diag-acl-client-ip',
						'type': 'text',
						'placeholder': '客户端 IP (如 192.168.1.150)',
						'style': 'flex: 1; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1; font-family: monospace; font-size: 13px;'
					}),
					E('input', {
						'id': 'oa-diag-acl-target',
						'type': 'text',
						'placeholder': '目标 (如 v.qq.com 或 router.lan)',
						'style': 'flex: 1; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1; font-family: monospace; font-size: 13px;'
					})
				]),
				E('button', {
					'id': 'btn-diag-acl-run',
					'class': 'btn cbi-button-action',
					'style': 'padding: 8px 18px; font-weight: 600; width: 100%;',
					'click': function() { self.runAclDiagnosis(); }
				}, '🚀 执行客户端链路透视诊断'),
				E('div', {
					'id': 'oa-diag-acl-result',
					'style': 'display: none; margin-top: 12px; background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 12px;'
				})
			])
		]);
		wrap.appendChild(botGrid);
		return wrap;
	},

	renderTabSub: function(cfg) {
		var self = this;
		var wrap = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 24px; border-radius: 12px; border: 1px solid #e2e8f0;' });
		wrap.appendChild(E('h3', { 'style': 'margin-top: 0; font-size: 18px; font-weight: 700;' }, '📥 机场订阅管理与节点拉取'));
		wrap.appendChild(E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 20px;' }, '支持 Clash YAML 订阅、V2Ray/Xray 订阅及 Sing-Box 格式链接，自动提取有效节点并持久化至本地。'));
		var currentUrl = '';
		if (cfg && cfg.subscriptions && cfg.subscriptions.length > 0) {
			currentUrl = cfg.subscriptions[0].url || '';
		}
		var inputGroup = E('div', { 'style': 'margin-bottom: 20px;' }, [
			E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 8px; font-size: 14px;' }, '订阅链接 (URL)'),
			E('input', {
				'id': 'oa-sub-url',
				'type': 'text',
				'value': currentUrl,
				'placeholder': 'https://domain.com/api/v1/client/subscribe?token=...',
				'style': 'width: 100%; padding: 10px 14px; border-radius: 8px; border: 1px solid #cbd5e1; font-size: 14px; font-family: monospace;'
			})
		]);
		wrap.appendChild(inputGroup);
		var btnRow = E('div', { 'style': 'display: flex; gap: 12px; margin-bottom: 16px;' }, [
			E('button', {
				'id': 'btn-update-sub',
				'class': 'btn cbi-button-action',
				'style': 'padding: 10px 24px; font-size: 14px; font-weight: 600;',
				'click': function() { self.updateSubscription(); }
			}, '🔄 一键在线更新订阅'),
			E('button', {
				'class': 'btn',
				'style': 'padding: 10px 20px; font-size: 14px;',
				'click': function() { self.saveSubUrlOnly(); }
			}, '💾 仅保存链接')
		]);
		wrap.appendChild(btnRow);
		// Async Progress Bar Card
		var progBox = E('div', { 'id': 'oa-sub-prog-box', 'style': 'display: none; background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 16px; margin-bottom: 20px;' }, [
			E('div', { 'id': 'oa-sub-prog-text', 'style': 'font-weight: 600; font-size: 13px; color: #1e293b; margin-bottom: 8px;' }, '正在准备拉取订阅...'),
			E('div', { 'style': 'width: 100%; height: 8px; background: #e2e8f0; border-radius: 4px; overflow: hidden;' }, [
				E('div', { 'id': 'oa-sub-prog-bar', 'style': 'width: 0%; height: 100%; background: #3b82f6; transition: width 0.3s ease;' })
			])
		]);
		wrap.appendChild(progBox);
		// Crontab Auto-Update Setting
		var selCron = E('select', { 'id': 'oa-sub-cron-interval', 'class': 'oa-select-touch', 'style': 'width: 100%; max-width: 380px;' }, [
			E('option', { 'value': '1' }, '⏰ 每天凌晨 04:30 自动静默更新 (推荐)'),
			E('option', { 'value': '2' }, '⏰ 每 2 天自动静默更新'),
			E('option', { 'value': '7' }, '⏰ 每周日凌晨 04:30 自动静默更新'),
			E('option', { 'value': '0' }, '🚫 关闭定时自动更新')
		]);
		selCron.value = (cfg && cfg.auto_update_interval !== undefined) ? ('' + cfg.auto_update_interval) : '1';
		selCron.onchange = function() {
			var val = parseInt(selCron.value);
			self.apiPost('/api/sub/cron', { interval: val })
				.then(function() {
					ui.addNotification(null, E('p', {}, '✅ 定时更新计划已同步生效至系统 Crontab！'), 'success');
				})
				.catch(function(err) {
					ui.addNotification(null, E('p', {}, '❌ 设置 Crontab 失败: ' + err.message), 'error');
				});
		};
		var cronSection = E('div', { 'style': 'margin-top: 20px; padding: 16px; background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px;' }, [
			E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 14px; color: #1e293b;' }, '⏰ 定时自动静默更新与健康自愈校验 (Crontab Schedule)'),
			E('div', { 'style': 'font-size: 12px; color: #64748b; margin-bottom: 12px;' }, '后台定期静默调用 openaero-core -update-subs -silent，自动执行金丝雀探针连通校验，失败自动回滚'),
			selCron
		]);
		wrap.appendChild(cronSection);
		if (cfg && cfg.subscriptions && cfg.subscriptions.length > 0) {
			var s = cfg.subscriptions[0];
			var infoCard = E('div', { 'style': 'margin-top: 24px; background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 16px; font-size: 13px; color: #475569;' }, [
				E('div', { 'style': 'font-weight: 600; margin-bottom: 6px; color: #1e293b;' }, '当前订阅状态：'),
				E('div', {}, '已托管节点：' + (s.node_count || (self.nodesCache && self.nodesCache.length) || 0) + ' 个'),
				E('div', {}, '最后更新时间：' + (s.last_updated || '刚刚'))
			]);
			wrap.appendChild(infoCard);
		}
		return wrap;
	},

	renderTabRules: function(cfg) {
		var self = this;
		var wrap = E('div', {});

		// 1. Pipeline Funnel
		var pipelineCard = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
			E('h3', { 'style': 'margin-top: 0; font-size: 16px; font-weight: 700; color: #0f172a;' }, '🧭 规则执行优先级漏斗 (Strict Top-Down Rule Pipeline)'),
			E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 16px;' }, '严格遵守「首条命中即止（First-Match Wins）」准则，按自顶向下严格匹配，杜绝环流与断网：'),
			E('div', { 'style': 'display: flex; flex-direction: column; gap: 8px;' }, [
				self.createPipelineStep('1️⃣ 梯队（最高优先级）', '🛡️ 底层防环流与私网保护', '私网网段 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) + 路由器本机 + 各节点 VPS 公网 IP', '🇨🇳 强制直连 (DIRECT)', '#10b981'),
				self.createPipelineStep('2️⃣ 梯队（自定义直连）', '⭐ 用户自定义白名单规则集 (my_whitelist)', 'QQ音乐、英雄联盟(LOL)、RiotGames、爱奇艺、陌陌等 26 条域名', '🇨🇳 优先直连 (DIRECT)', '#10b981'),
				self.createPipelineStep('3️⃣ 梯队（自定义代理）', '⭐ 用户自定义代理规则集 (my_proxylist)', 'anyrouter.top, chengmo.cc.cd, this52.cn, sb.sb 等必须出海代理域名', '🌐 强制代理 (MATCH)', '#3b82f6'),
				self.createPipelineStep('4️⃣ 梯队（业务分流）', '🎯 专线策略分流组 (Gemini / ChatGPT / YouTube / Telegram / Google)', '绑定对应独立出站节点（含 google_play, gvt1 专属分流）', '🔀 策略组绑定出站', '#8b5cf6'),
				self.createPipelineStep('5️⃣ 梯队（大陆直连）', '🇨🇳 中国大陆域名与 IP 直连', 'geosite-cn (国内主流域名库) + geoip-cn (国内大陆 IP 库)', '🇨🇳 国内直连 (DIRECT)', '#10b981'),
				self.createPipelineStep('6️⃣ 梯队（最低兜底）', '🌐 漏网之鱼兜底 (MATCH)', '其余所有未命中上述规则的境外未知域名与全部流量', '🌐 漏网之鱼默认出站', '#f59e0b')
			])
		]);
		wrap.appendChild(pipelineCard);
		// 2. User's Rule Providers (my_proxylist, my_whitelist, google_play, gvt1, etc.)
		var providers = cfg.rule_providers || [];
		var provCard = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
			E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; flex-wrap: wrap; gap: 10px;' }, [
				E('div', {}, [
					E('h3', { 'style': 'margin: 0 0 4px 0; font-size: 16px; font-weight: 700; color: #0f172a;' }, '⭐ 订阅与用户自定义规则集 (Rule Providers)'),
					E('div', { 'style': 'font-size: 12px; color: #64748b;' }, '源自订阅文件与个人 GitHub 维护仓库（如 Spittingjiu/mihomo-custom-rules），自动同步生效')
				]),
				E('button', {
					'class': 'btn cbi-button-action btn-sm oa-ctrl-btn',
					'style': 'padding: 6px 16px; font-weight: 600;',
					'click': function() { self.updateRuleSets(); }
				}, '🔄 一键在线同步规则集')
			])
		]);
		var provGrid = E('div', { 'class': 'oa-group-grid' });
		if (providers.length === 0) {
			provGrid.appendChild(E('div', { 'style': 'color: #94a3b8; font-size: 13px; padding: 10px;' }, '暂未检测到规则集，可点击上方“一键在线同步规则集”或在“订阅管理”更新订阅自动拉取。'));
		} else {
			providers.forEach(function(p) {
				var targetColor = '#10b981';
				var targetText = '🇨🇳 国内直连 (DIRECT)';
				if (p.target === 'match' || p.target === 'proxy') {
					targetColor = '#3b82f6';
					targetText = '🚀 漏网之鱼/节点选择 (MATCH)';
				} else if (p.target === 'REJECT') {
					targetColor = '#ef4444';
					targetText = '🛑 拦截阻断 (REJECT)';
				} else if (p.target === 'google') {
					targetColor = '#8b5cf6';
					targetText = '🔍 Google 策略组出站';
				} else if (p.target === 'gemini') {
					targetColor = '#8b5cf6';
					targetText = '♊ Gemini 策略组出站';
				} else if (p.target === 'chatgpt') {
					targetColor = '#8b5cf6';
					targetText = '🤖 ChatGPT 策略组出站';
				} else if (p.target === 'youtube') {
					targetColor = '#8b5cf6';
					targetText = '📺 YouTube 策略组出站';
				} else if (p.target === 'telegram') {
					targetColor = '#8b5cf6';
					targetText = '✈️ Telegram 策略组出站';
				}
				var pCard = E('div', {
					'style': 'background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 10px; padding: 14px 16px; display: flex; flex-direction: column; justify-content: space-between;'
				}, [
					E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;' }, [
						E('div', { 'style': 'font-weight: 700; font-size: 14px; color: #1e293b; display: flex; align-items: center; gap: 6px;' }, [
							E('span', {}, '⭐'),
							p.name || p.id
						]),
						E('span', { 'style': 'font-size: 11px; background: #e2e8f0; color: #475569; padding: 2px 8px; border-radius: 10px;' }, (p.count || 0) + ' 条')
					]),
					E('div', { 'style': 'font-size: 11px; font-family: monospace; color: #64748b; margin-bottom: 10px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;' }, p.url || '本地规则集'),
					E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center;' }, [
						E('span', { 'style': 'font-size: 11px; color: #94a3b8;' }, '更新: ' + (p.updated || '-')),
						E('span', { 'style': 'font-size: 12px; font-weight: 700; color: ' + targetColor + ';' }, targetText)
					])
				]);
				provGrid.appendChild(pCard);
			});
		}
		provCard.appendChild(provGrid);
		wrap.appendChild(provCard);
		// 3. System Binary SRS Rule sets status
		var srsCard = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
			E('div', { 'style': 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 15px;' }, [
				E('h3', { 'style': 'margin: 0; font-size: 16px; font-weight: 700;' }, '📦 系统底层二进制分流规则库 (Sing-Box SRS)'),
				E('span', { 'style': 'font-size: 12px; color: #64748b;' }, '高性能无损热加载')
			]),
			E('div', { 'style': 'display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 10px;' }, [
				self.createRuleBadge('geosite-cn.srs', '440 KB', '国内主流域名直连'),
				self.createRuleBadge('geoip-cn.srs', '78 KB', '国内 IP 网段直连'),
				self.createRuleBadge('geosite-category-games@cn.srs', '38 KB', '国内热门游戏直连'),
				self.createRuleBadge('geosite-steam@cn.srs', '12 KB', 'Steam 国区下载加速'),
				self.createRuleBadge('geosite-apple@cn.srs', '24 KB', 'Apple 官方服务直连'),
				self.createRuleBadge('geosite-microsoft@cn.srs', '32 KB', '微软基础服务直连'),
				self.createRuleBadge('geosite-google-gemini.srs', '1.8 KB', 'Gemini AI 智能分流'),
				self.createRuleBadge('geosite-openai.srs', '2.4 KB', 'ChatGPT / OpenAI 专属'),
				self.createRuleBadge('geosite-google.srs', '8.2 KB', 'Google 服务专属'),
				self.createRuleBadge('geosite-youtube.srs', '1.6 KB', 'YouTube 媒体专用'),
				self.createRuleBadge('geosite-telegram.srs', '1.1 KB', 'Telegram 专属分流')
			])
		]);
		wrap.appendChild(srsCard);
		// Single domain quick-add
		var quickAddCard = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0; margin-bottom: 24px;' }, [
			E('h3', { 'style': 'margin-top: 0; font-size: 16px; font-weight: 700;' }, '➕ 快速添加单一域名规则'),
			E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 15px;' }, '输入单一域名（例如 baidu.com 或 openai.com），选择让它走国内直连、走海外代理或绑定到指定策略组。'),
			E('div', { 'style': 'display: flex; gap: 12px; flex-wrap: wrap; align-items: center;' }, [
				E('input', {
					'id': 'oa-quick-domain',
					'type': 'text',
					'placeholder': '例如: baidu.com 或 custom.domain',
					'style': 'flex: 2; min-width: 220px; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1; font-size: 14px;'
				}),
				E('select', {
					'id': 'oa-quick-target',
					'style': 'flex: 1; min-width: 180px; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1; font-size: 14px;'
				}, [
					E('option', { 'value': 'direct' }, '🇨🇳 国内直连 (Direct)'),
					E('option', { 'value': 'proxy' }, '🚀 国际代理 (GLOBAL)'),
					E('option', { 'value': 'gemini' }, '♊ Gemini 策略组'),
					E('option', { 'value': 'chatgpt' }, '🤖 ChatGPT / OpenAI 策略组'),
					E('option', { 'value': 'google' }, '🔍 Google 服务策略组'),
					E('option', { 'value': 'youtube' }, '📺 YouTube 策略组'),
					E('option', { 'value': 'telegram' }, '✈️ Telegram 策略组')
				]),
				E('button', {
					'class': 'btn cbi-button-action',
					'style': 'padding: 8px 20px; font-weight: 600;',
					'click': function() {
						var dom = document.getElementById('oa-quick-domain').value.trim();
						var tgt = document.getElementById('oa-quick-target').value;
						if (!dom) return;
						ui.showIndicator();
						self.apiPost('/api/rules/add', { domain: dom, target: tgt })
							.then(function() {
								ui.hideIndicator();
								ui.addNotification(null, E('p', {}, '✅ 域名 [' + dom + '] 已添加生效！'), 'success');
								document.getElementById('oa-quick-domain').value = '';
							})
							.catch(function(err) {
								ui.hideIndicator();
								ui.addNotification(null, E('p', {}, '❌ 添加失败: ' + err.message), 'error');
							});
					}
				}, '添加到规则库')
			])
		]);
		wrap.appendChild(quickAddCard);
		// Custom Domains List
		var directDomains = (cfg && cfg.custom_direct_domains) ? cfg.custom_direct_domains.join('\n') : '';
		var proxyDomains = (cfg && cfg.custom_proxy_domains) ? cfg.custom_proxy_domains.join('\n') : '';
		var customCard = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 20px; border-radius: 12px; border: 1px solid #e2e8f0;' }, [
			E('h3', { 'style': 'margin-top: 0; font-size: 16px; font-weight: 700;' }, '✏️ 批量自定义域名清单'),
			E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 15px;' }, '每行一个主域名（例如 baidu.com），支持随时编辑并保存。'),
			E('div', { 'style': 'display: grid; grid-template-columns: 1fr 1fr; gap: 20px;' }, [
				E('div', {}, [
					E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, '🇨🇳 自定义直连域名 (Direct)'),
					E('textarea', { 'id': 'oa-custom-direct', 'rows': '8', 'style': 'width: 100%; border-radius: 8px; border: 1px solid #cbd5e1; padding: 8px; font-family: monospace; font-size: 13px;' }, directDomains)
				]),
				E('div', {}, [
					E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, '🚀 自定义代理域名 (Proxy)'),
					E('textarea', { 'id': 'oa-custom-proxy', 'rows': '8', 'style': 'width: 100%; border-radius: 8px; border: 1px solid #cbd5e1; padding: 8px; font-family: monospace; font-size: 13px;' }, proxyDomains)
				])
			]),
			E('div', { 'style': 'margin-top: 15px;' }, [
				E('button', {
					'class': 'btn cbi-button-apply',
					'click': function() { self.saveCustomRules(); }
				}, '💾 保存自定义域名清单')
			])
		]);
		wrap.appendChild(customCard);
		return wrap;
	},

	renderTabCore: function(status) {
		var self = this;
		var wrap = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 24px; border-radius: 12px; border: 1px solid #e2e8f0;' });
		wrap.appendChild(E('h3', { 'style': 'margin-top: 0; font-size: 18px; font-weight: 700;' }, '🔄 sing-box 内核管理与在线升级'));
		wrap.appendChild(E('p', { 'style': 'color: #64748b; font-size: 13px; margin-bottom: 20px;' }, '自动比对 GitHub 官方最新发布版本，一键无损热替换 ARM64 原生二进制。'));
		var verBox = E('div', { 'style': 'background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 16px; margin-bottom: 20px;' }, [
			E('div', { 'style': 'font-size: 14px; font-weight: 600; color: #1e293b; margin-bottom: 4px;' }, '当前运行内核：' + (status.singbox_version || 'sing-box 1.13.14 (ARM64)')),
			E('div', { 'style': 'font-size: 13px; color: #64748b;' }, '架构: aarch64 (Linux ARM64 Cortex-A53 / RK3568) · 编译特性: with_quic, with_wireguard, with_gvisor, with_ech')
		]);
		wrap.appendChild(verBox);
		var btnRow = E('div', { 'style': 'display: flex; gap: 12px;' }, [
			E('button', {
				'id': 'btn-core-update',
				'class': 'btn cbi-button-action',
				'style': 'padding: 10px 24px; font-weight: 600;',
				'click': function() { self.updateCore(); }
			}, '⚡ 一键在线更新 sing-box 内核'),
			E('button', {
				'class': 'btn',
				'style': 'padding: 10px 20px;',
				'click': function() { self.refreshStatus(); }
			}, '🔍 检查更新')
		]);
		wrap.appendChild(btnRow);
		var logArea = E('pre', { 'id': 'oa-core-update-log', 'style': 'margin-top: 20px; background: #1e1e1e; color: #a3e635; padding: 14px; border-radius: 8px; font-size: 12px; display: none; max-height: 200px; overflow-y: auto;' });
		wrap.appendChild(logArea);
		return wrap;
	},

	renderTabSettings: function(cfg) {
		var self = this;
		var wrap = E('div', { 'class': 'cbi-section', 'style': 'background: #fff; padding: 24px; border-radius: 12px; border: 1px solid #e2e8f0;' });
		wrap.appendChild(E('h3', { 'style': 'margin-top: 0; font-size: 18px; font-weight: 700;' }, '⚙️ OpenAero 全局核心参数'));
		var selMode = E('select', { 'id': 'oa-cfg-mode', 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' }, [
			E('option', { 'value': 'rule' }, '🎯 智能分流 (Rule 模式 - 推荐)'),
			E('option', { 'value': 'global' }, '🚀 全局代理 (Global 模式)'),
			E('option', { 'value': 'direct' }, '🇨🇳 全局直连 (Direct 模式)')
		]);
		selMode.value = (cfg.mode === 'global' || cfg.mode === 'direct') ? cfg.mode : 'rule';
		var selIpv6 = E('select', { 'id': 'oa-cfg-ipv6-mode', 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' }, [
			E('option', { 'value': 'drop' }, '🛡️ 智能防泄漏 (推荐 - 阻断海外 AAAA / 国内放行原生 IPv6)'),
			E('option', { 'value': 'proxy' }, '🌐 全双栈代理 (TUN 启用 IPv6 网段并全量接管)'),
			E('option', { 'value': 'disable' }, '🚫 全局禁用 IPv6 (彻底丢弃所有 AAAA 解析)')
		]);
		selIpv6.value = cfg.ipv6_mode || (cfg.ipv6_enabled ? 'proxy' : 'drop');
		var selSm = E('select', { 'id': 'oa-cfg-sm-mode', 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' }, [
			E('option', { 'value': 'follow_node' }, '⚡ 跟随当前主节点 (推荐 - 自动对齐节点 IP)'),
			E('option', { 'value': 'custom' }, '🔧 自定义预热 VPS 服务器'),
			E('option', { 'value': 'disabled' }, '⏹️ 停用 0-RTT 预热')
		]);
		selSm.value = ((cfg.ortt_mode || cfg.sing_matrix_mode) === 'custom' || (cfg.ortt_mode || cfg.sing_matrix_mode) === 'disabled') ? (cfg.ortt_mode || cfg.sing_matrix_mode) : 'follow_node';
		var selDnsMode = E('select', { 'id': 'oa-cfg-dns-mode', 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' }, [
			E('option', { 'value': 'fake-ip' }, '🚀 Fake-IP 极速模式 (0-RTT 推荐 - 虚拟网段 198.18.0.0/15)'),
			E('option', { 'value': 'redir-host' }, '⚡ Redir-Host 模式 (真实解析兼容)')
		]);
		selDnsMode.value = (cfg.dns_mode === 'redir-host') ? 'redir-host' : 'fake-ip';
		var formGrid = E('div', { 'class': 'oa-form-grid', 'style': 'display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 18px; margin-bottom: 24px;' }, [
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, '分流运行模式'),
				selMode
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, 'DNS 解析加速模式'),
				selDnsMode
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, 'Fake-IP 虚拟网段 (仅 Fake-IP 模式)'),
				E('input', { 'id': 'oa-cfg-fake-ip-range', 'type': 'text', 'value': cfg.fake_ip_range || '198.18.0.0/15', 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' })
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, 'IPv6 代理与解析开关'),
				selIpv6
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, 'ORTT 0-RTT 预热模式'),
				selSm
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, '自定义 ORTT 预热节点 (仅自定义模式)'),
				E('input', { 'id': 'oa-cfg-vps', 'type': 'text', 'value': (cfg.ortt_vps || cfg.sing_matrix_vps) || '', 'placeholder': '可选，留空则自动跟随节点', 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' })
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, '本地 SOCKS5 端口'),
				E('input', { 'id': 'oa-cfg-socks', 'type': 'number', 'value': cfg.socks_port || 10808, 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' })
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, '本地 HTTP 代理端口'),
				E('input', { 'id': 'oa-cfg-http', 'type': 'number', 'value': cfg.http_port || 10809, 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' })
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, 'DNS 劫持转发端口'),
				E('input', { 'id': 'oa-cfg-dns', 'type': 'number', 'value': cfg.dns_port || 1053, 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' })
			]),
			E('div', {}, [
				E('label', { 'style': 'display: block; font-weight: 600; margin-bottom: 6px; font-size: 13px;' }, '防断网自愈心跳探测周期 (秒)'),
				E('input', { 'id': 'oa-cfg-wd-int', 'type': 'number', 'value': cfg.watchdog_interval || 5, 'style': 'width: 100%; padding: 8px 12px; border-radius: 8px; border: 1px solid #cbd5e1;' })
			])
		]);
		wrap.appendChild(formGrid);
		var saveBtn = E('button', {
			'class': 'btn cbi-button-apply',
			'style': 'padding: 10px 28px; font-size: 14px; font-weight: 600;',
			'click': function() { self.saveGlobalSettings(); }
		}, '💾 保存全局配置并热应用');
		wrap.appendChild(saveBtn);
		return wrap;
	},

	createCard: function(title, value, sub, accentColor, valId, subId) {
		var valProps = { 'style': 'font-size: 20px; font-weight: 700; color: #1e293b; margin-bottom: 4px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;' };
		if (valId) valProps.id = valId;
		var subProps = { 'style': 'font-size: 12px; color: #94a3b8;' };
		if (subId) subProps.id = subId;
		return E('div', {
			'style': 'background: #fff; border-radius: 12px; padding: 18px; border: 1px solid #e2e8f0; border-top: 4px solid ' + accentColor + '; box-shadow: 0 2px 6px rgba(0,0,0,0.03);'
		}, [
			E('div', { 'style': 'font-size: 13px; color: #64748b; font-weight: 600; margin-bottom: 6px;' }, title),
			E('div', valProps, value),
			E('div', subProps, sub)
		]);
	},

	createRuleBadge: function(name, size, desc) {
		return E('div', { 'style': 'background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 10px 12px; display: flex; justify-content: space-between; align-items: center;' }, [
			E('div', {}, [
				E('div', { 'style': 'font-weight: 600; font-size: 13px; color: #1e293b;' }, name),
				E('div', { 'style': 'font-size: 11px; color: #64748b;' }, desc)
			]),
			E('span', { 'style': 'font-size: 11px; background: #e2e8f0; padding: 2px 6px; border-radius: 4px; font-family: monospace;' }, size)
		]);
	},

	checkBypassStatus: function() {
		var self = this;
		self.apiGet('/api/bypass/status').then(function(res) {
			var badge = document.getElementById('oa-bypass-badge');
			var statusText = document.getElementById('oa-bypass-status-text');
			if (!badge || !statusText) return;
			if (res.enabled) {
				badge.textContent = '● 旁路网关已全网接管 (Option 3 & 6: ' + res.router_ip + ')';
				badge.style.background = '#dcfce7';
				badge.style.color = '#15803d';
				statusText.innerHTML = '<span style="color: #16a34a; font-weight: 600;">✅ 局域网拓扑健康闭环</span>：本机 IP <code style="background:#e2e8f0;padding:1px 4px;border-radius:4px;">' + res.router_ip + '</code> 已作为默认网关与首选 DNS 成功写入 DHCP Option 3 与 Option 6，全网终端免手动配置即可享受 Fake-IP 0-RTT 加速。';
			} else {
				badge.textContent = '● 未启用接管 (主路由分配网关)';
				badge.style.background = '#fef3c7';
				badge.style.color = '#d97706';
				var warn = res.warning || '局域网终端默认网关当前由主路由分配。';
				statusText.innerHTML = '<span style="color: #d97706; font-weight: 600;">⚠️ ' + warn + '</span> 若终端需走代理，请点击上方【一键接管局域网 DHCP 网关与 DNS】，或手动将终端网关与 DNS 同时设置为 <code style="background:#e2e8f0;padding:1px 4px;border-radius:4px;">' + res.router_ip + '</code>。';
			}
		}).catch(function(e) {
			console.error('checkBypassStatus error', e);
		});
	},

	toggleBypassMode: function(enable) {
		var self = this;
		ui.showIndicator();
		self.apiPost('/api/bypass/toggle', { enable: enable }).then(function(res) {
			ui.hideIndicator();
			ui.addNotification(null, E('p', (enable ? '✅ 成功启用旁路网关一键接管！' : '🔄 已恢复默认 DHCP 配置。')), 'info');
			self.checkBypassStatus();
		}).catch(function(e) {
			ui.hideIndicator();
			ui.addNotification(null, E('p', '操作失败: ' + (e.message || e)), 'error');
		});
	},

	// Interactive Actions
	controlService: function(action) {
		var self = this;
		ui.showIndicator();
		self.apiPost('/api/service/action', { action: action })
			.then(function() {
				setTimeout(function() {
					ui.hideIndicator();
					ui.addNotification(null, E('p', {}, '✅ 服务已触发 ' + action), 'success');
					self.refreshStatus();
					self.fetchLogs();
				}, 1500);
			})
			.catch(function(err) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '❌ 控制失败: ' + err.message), 'error');
			});
	},

	testAllNodesOrtt: function() {
		var self = this;
		ui.showIndicator();
		self.apiPost('/api/nodes/ortt_test', {})
			.then(function(res) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '🔍 全节点 ORTT 全链路公正检测已完成！'), 'success');
				self.refreshNodes();
			})
			.catch(function(err) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '❌ ORTT 检测失败: ' + (err.message || err)), 'error');
			});
	},

	testSingleNodeOrtt: function(nodeId) {
		var self = this;
		ui.showIndicator();
		self.apiPost('/api/nodes/ortt_test', { id: nodeId })
			.then(function(res) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '🔍 节点 ORTT 检测已完成！'), 'success');
				self.refreshNodes();
			})
			.catch(function(err) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '❌ 节点 ORTT 检测失败: ' + (err.message || err)), 'error');
			});
	},

	benchmarkAllNodes: function() {
		var self = this;
		ui.showIndicator();
		self.apiPost('/api/nodes/test', {})
			.then(function(res) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '⚡ 全节点并发测速已完成！'), 'success');
				self.refreshNodes();
				self.refreshStatus();
			})
			.catch(function(err) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '❌ 测速失败: ' + err.message), 'error');
			});
	},

	selectFastestHealthyNode: function() {
		var self = this;
		var list = self.nodesCache || [];
		if (list.length === 0) {
			ui.addNotification(null, E('p', {}, '节点列表为空，无法优选节点'), 'warning');
			return;
		}

		var isInfo = function(n) {
			var r = (n.remark || '').toLowerCase();
			return r.indexOf('剩余流量') !== -1 || r.indexOf('到期时间') !== -1 || r.indexOf('套餐') !== -1 || r.indexOf('官网') !== -1;
		};

		var candidates = list.filter(function(n) {
			return !isInfo(n) && n.latency > 0;
		});

		if (candidates.length === 0) {
			ui.addNotification(null, E('p', {}, '未检测到有效延迟的节点，请先点击「⚡ 一键全节点测速」！'), 'warning');
			return;
		}

		var healthyCandidates = candidates.filter(function(n) {
			return n.ortt_status === 'healthy';
		});

		var pool = (healthyCandidates.length > 0) ? healthyCandidates : candidates;
		pool.sort(function(a, b) {
			return a.latency - b.latency;
		});

		var fastest = pool[0];
		self.selectActiveNode(fastest.id);
	},

	refreshNodes: function() {
		var self = this;
		self.apiGet('/api/nodes').then(function(nodes) {
			self.nodesCache = nodes || [];
			self.apiGet('/api/status').then(function(st) {
				self.renderNodeCards(self.nodesCache, st.active_node, self.currentFilterRegion || 'all', self.currentSearchKeyword || '');
			});
		});
	},

	selectActiveNode: function(nodeId) {
		var self = this;
		if (self.isSwitching) return;
		self.isSwitching = true;
		var targetNode = (self.nodesCache || []).find(function(n) { return n.id === nodeId; });
		var targetRemark = targetNode ? targetNode.remark : nodeId;
		self.apiPost('/api/node/hot_select', { node_id: nodeId, group: 'GLOBAL' })
			.then(function(res) {
				self.isSwitching = false;
				self.activeNodeId = nodeId;
				ui.addNotification(null, E('p', {}, '⚡ 已极速热切换至节点: ' + (res.active_node || targetRemark) + (res.time_ms !== undefined ? (' (' + res.time_ms + 'ms)') : '')), 'success');
				self.refreshStatus();
				self.refreshNodes();
			})
			.catch(function(err) {
				self.isSwitching = false;
				ui.addNotification(null, E('p', {}, '❌ 切换失败: ' + err.message), 'error');
				self.refreshNodes();
			});
	},

	saveSubUrlOnly: function() {
		var self = this;
		var urlEl = document.getElementById('oa-sub-url');
		var url = urlEl ? urlEl.value.trim() : '';
		if (!url) {
			ui.addNotification(null, E('p', {}, '请先输入有效的订阅链接！'), 'warning');
			return;
		}
		ui.showIndicator();
		self.apiGet('/api/config').then(function(cfg) {
			cfg = cfg || {};
			if (!cfg.subscriptions || cfg.subscriptions.length === 0) {
				cfg.subscriptions = [{ id: 'sub-1', name: '默认订阅', url: url, enabled: true }];
			} else {
				cfg.subscriptions[0].url = url;
			}
			return self.apiPost('/api/config', cfg);
		}).then(function() {
			ui.hideIndicator();
			ui.addNotification(null, E('p', {}, '✅ 订阅链接已成功保存！'), 'success');
		}).catch(function(err) {
			ui.hideIndicator();
			ui.addNotification(null, E('p', {}, '❌ 保存订阅链接失败: ' + err.message), 'error');
		});
	},

	updateSubscription: function() {
		var self = this;
		var url = document.getElementById('oa-sub-url').value.trim();
		if (!url) {
			ui.addNotification(null, E('p', {}, '请先输入有效的订阅链接！'), 'warning');
			return;
		}
		var btn = document.getElementById('btn-update-sub');
		if (btn) btn.disabled = true;
		var progBox = document.getElementById('oa-sub-prog-box');
		var progBar = document.getElementById('oa-sub-prog-bar');
		var progText = document.getElementById('oa-sub-prog-text');
		if (progBox) progBox.style.display = 'block';
		self.apiPost('/api/sub/async_update', { url: url })
			.then(function() {
				var timer = setInterval(function() {
					self.apiGet('/api/sub/progress').then(function(p) {
						if (!p) return;
						if (progBar) progBar.style.width = (p.percent || 10) + '%';
						if (progText) progText.textContent = (p.percent || 0) + '% - ' + (p.message || '正在更新...');
						if (p.state === 'SUCCESS') {
							clearInterval(timer);
							if (btn) btn.disabled = false;
							ui.addNotification(null, E('p', {}, '✅ ' + p.message), 'success');
							self.refreshStatus();
							self.refreshNodes();
							setTimeout(function() { if (progBox) progBox.style.display = 'none'; }, 4000);
						} else if (p.state === 'FAILED') {
							clearInterval(timer);
							if (btn) btn.disabled = false;
							ui.addNotification(null, E('p', {}, '❌ 订阅更新失败: ' + (p.last_error || p.message)), 'error');
						}
					}).catch(function() {
						clearInterval(timer);
						if (btn) btn.disabled = false;
					});
				}, 600);
			})
			.catch(function(err) {
				if (btn) btn.disabled = false;
				ui.addNotification(null, E('p', {}, '❌ 发起更新失败: ' + err.message), 'error');
			});
	},

	updateRuleSets: function() {
		var self = this;
		ui.showIndicator();
		self.apiPost('/api/rules/update', {})
			.then(function() {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '✅ 核心分流规则集已更新成功！'), 'success');
			})
			.catch(function(err) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '❌ 规则库更新失败: ' + err.message), 'error');
			});
	},

	updateCore: function() {
		var self = this;
		var logEl = document.getElementById('oa-core-update-log');
		if (logEl) {
			logEl.style.display = 'block';
			logEl.innerText = '正在连接 GitHub Releases 检查并下载 sing-box ARM64 最新核心...';
		}
		self.apiPost('/api/core/update', {})
			.then(function(res) {
				if (logEl) logEl.innerText += '\n✅ 更新成功！当前新版本: ' + res.version + '\n已自动重启核心生效。';
				ui.addNotification(null, E('p', {}, '✅ 内核升级成功: ' + res.version), 'success');
				self.refreshStatus();
			})
			.catch(function(err) {
				if (logEl) logEl.innerText += '\n❌ 升级失败: ' + err.message;
				ui.addNotification(null, E('p', {}, '❌ 内核更新失败: ' + err.message), 'error');
			});
	},

	saveCustomRules: function() {
		var self = this;
		var d = document.getElementById('oa-custom-direct').value.split('\n').map(function(s) { return s.trim(); }).filter(Boolean);
		var p = document.getElementById('oa-custom-proxy').value.split('\n').map(function(s) { return s.trim(); }).filter(Boolean);
		self.apiGet('/api/config').then(function(cfg) {
			cfg.custom_direct_domains = d;
			cfg.custom_proxy_domains = p;
			return self.apiPost('/api/config', cfg);
		}).then(function() {
			ui.addNotification(null, E('p', {}, '✅ 自定义域名规则已保存并重载！'), 'success');
		}).catch(function(err) {
			ui.addNotification(null, E('p', {}, '❌ 保存失败: ' + err.message), 'error');
		});
	},

	saveGlobalSettings: function() {
		var self = this;
		var modeEl = document.getElementById('oa-cfg-mode');
		var ipv6ModeEl = document.getElementById('oa-cfg-ipv6-mode');
		var smModeEl = document.getElementById('oa-cfg-sm-mode');
		var vpsEl = document.getElementById('oa-cfg-vps');
		var socksEl = document.getElementById('oa-cfg-socks');
		var httpEl = document.getElementById('oa-cfg-http');
		var dnsEl = document.getElementById('oa-cfg-dns');
		var wdIntEl = document.getElementById('oa-cfg-wd-int');
		var dnsModeEl = document.getElementById('oa-cfg-dns-mode');
		var fakeIpRangeEl = document.getElementById('oa-cfg-fake-ip-range');
		var mode = modeEl ? modeEl.value : 'rule';
		var dnsMode = dnsModeEl ? dnsModeEl.value : 'fake-ip';
		var fakeIpRange = fakeIpRangeEl ? fakeIpRangeEl.value.trim() : '198.18.0.0/15';
		var ipv6Mode = ipv6ModeEl ? ipv6ModeEl.value : 'drop';
		var ipv6 = (ipv6Mode === 'proxy');
		var smMode = smModeEl ? smModeEl.value : 'follow_node';
		var vps = vpsEl ? vpsEl.value.trim() : '';
		var socks = (socksEl && parseInt(socksEl.value)) || 10808;
		var http = (httpEl && parseInt(httpEl.value)) || 10809;
		var dns = (dnsEl && parseInt(dnsEl.value)) || 1053;
		var wdInt = (wdIntEl && parseInt(wdIntEl.value)) || 5;
		ui.showIndicator();
		return self.apiGet('/api/config').then(function(cfg) {
			cfg.mode = mode;
			cfg.dns_mode = dnsMode;
			cfg.fake_ip_range = fakeIpRange;
			cfg.ipv6_mode = ipv6Mode;
			cfg.ipv6_enabled = ipv6;
			cfg.ortt_mode = smMode;
			cfg.sing_matrix_mode = smMode;
			cfg.ortt_vps = vps;
			cfg.sing_matrix_vps = vps;
			cfg.socks_port = socks;
			cfg.http_port = http;
			cfg.dns_port = dns;
			cfg.watchdog_interval = wdInt;
			return self.apiPost('/api/config', cfg);
		}).then(function(res) {
			ui.hideIndicator();
			if (res && res.rolled_back) {
				ui.addNotification(null, E('p', {}, '⚠️ ' + (res.error || '新配置未能通过连通性自检，系统已自动安全回退！')), 'warning');
			} else {
				ui.addNotification(null, E('p', {}, '✅ 全局核心参数已保存并通过外网健康校验！'), 'success');
			}
			self.refreshStatus();
			self.fetchLogs();
		}).catch(function(err) {
			ui.hideIndicator();
			ui.addNotification(null, E('p', {}, '❌ 保存全局参数失败: ' + err.message), 'error');
		});
	},

	applyStatus: function(st) {
		var self = this;
		if (!st) return;
		if (st.active_node_id) {
			self.activeNodeId = st.active_node_id;
		} else if (st.active_node && st.active_node.id) {
			self.activeNodeId = st.active_node.id;
		}
		var badgeBox = document.getElementById('oa-header-badge');
		if (badgeBox) {
			badgeBox.innerHTML = '';
			badgeBox.appendChild(self.renderCoreBadge(st));
		}
		var nodeVal = document.getElementById('oa-stat-node-val');
		var nodeSub = document.getElementById('oa-stat-node-sub');
		if (nodeVal && st.active_node) {
			nodeVal.innerText = st.active_node.remark || '未指定';
			var lat = (st.active_node.latency > 0) ? (st.active_node.latency + ' ms') : '未测速';
			var proto = st.active_node.type ? st.active_node.type.toUpperCase() : '-';
			if (nodeSub) nodeSub.innerText = '延迟: ' + lat + ' · ' + proto;
		}
		var procVal = document.getElementById('oa-stat-proc-val');
		var procSub = document.getElementById('oa-stat-proc-sub');
		if (procVal) {
			procVal.innerText = st.singbox_running ? ('PID: ' + st.singbox_pid) : '停止';
			if (procSub) procSub.innerText = '内存占用: ' + (st.singbox_mem || '0 MB');
		}
		var wdVal = document.getElementById('oa-stat-wd-val');
		if (wdVal) {
			wdVal.innerText = (st.watchdog_status === 'running') ? '激活中' : '未启用';
		}
		var smVal = document.getElementById('oa-stat-sm-val');
		var smSubEl = document.getElementById('oa-stat-sm-sub');
		if (smVal) {
			var curOrttMode = st.ortt_mode || st.sing_matrix_mode || 'follow_node';
			var curOrttTarget = st.ortt_target || st.sing_matrix_target;
			var actN = st.active_node;
			if (curOrttMode === 'disabled') {
				smVal.innerText = '已停用';
				if (smSubEl) smSubEl.innerText = '可在全局设置开启';
			} else if (actN && (actN.ortt_warm_conns > 0 || actN.ortt_status === 'healthy')) {
				var hr = actN.ortt_hit_rate || '0%';
				smVal.innerText = '🟢 热池: ' + (actN.ortt_warm_conns || 0) + ' · 命中率 ' + hr;
				var hits = (actN.ortt_pool_hits != null) ? actN.ortt_pool_hits : 0;
				var serverAddr = actN.server ? (actN.server + ':39999') : (curOrttTarget || '39999');
				if (smSubEl) smSubEl.innerText = serverAddr + ' · 累计命中 ' + hits + ' 次';
			} else if (curOrttMode === 'follow_node') {
				var actRem = (actN && actN.remark) ? (' (' + actN.remark.replace(/^.*?- /,'') + ')') : '';
				smVal.innerText = '跟随活跃节点' + actRem;
				if (smSubEl) smSubEl.innerText = curOrttTarget || '端口: 39999';
			} else if (curOrttMode === 'custom') {
				smVal.innerText = '自定义中继';
				if (smSubEl) smSubEl.innerText = curOrttTarget || '自定义地址';
			}
		}
	},

	refreshStatus: function() {
		var self = this;
		return self.apiGet('/api/status').then(function(st) {
			self.applyStatus(st);
		}).catch(function() {});
	},

	applyLogs: function(res) {
		var box = document.getElementById('oa-log-box');
		var statusText = document.getElementById('oa-log-status');
		if (!box) return;
		var logList = (res && res.logs) ? res.logs : (Array.isArray(res) ? res : []);
		if (logList.length === 0) {
			box.innerText = '暂无内核日志输出（核心正常静默运行中）';
		} else {
			var cleanLogs = logList.map(function(l) {
				return l.replace(/\x1b\[[0-9;]*[a-zA-Z]/g, '');
			});
			var isAtBottom = (box.scrollHeight - box.clientHeight <= box.scrollTop + 40);
			box.innerText = cleanLogs.join('\n');
			if (isAtBottom) {
				box.scrollTop = box.scrollHeight;
			}
		}
		if (statusText) statusText.innerHTML = '<span style="color: #10b981;">●</span> 实时读取中 (' + logList.length + ' 行)';
	},

	fetchLogs: function() {
		var self = this;
		var box = document.getElementById('oa-log-box');
		var statusText = document.getElementById('oa-log-status');
		if (!box || self.isFetchingLogs) return;
		self.isFetchingLogs = true;
		self.apiGet('/api/logs').then(function(res) {
			self.isFetchingLogs = false;
			self.applyLogs(res);
		}).catch(function(err) {
			self.isFetchingLogs = false;
			if (box && (box.innerText.indexOf('正在') !== -1 || box.innerText === '')) {
				box.innerText = '等待日志输出或尝试点击“刷新日志”... (' + (err && err.message ? err.message : '网络连接中') + ')';
			}
			if (statusText) statusText.innerHTML = '<span style="color: #f59e0b;">●</span> 稍后重试';
		});
	},

	getFlagEmoji: function(remark, server) {
		var txt = ((remark || '') + ' ' + (server || '')).toLowerCase();
		if (txt.indexOf('hk') !== -1 || txt.indexOf('香港') !== -1) return '🇭🇰';
		if (txt.indexOf('jp') !== -1 || txt.indexOf('日本') !== -1 || txt.indexOf('东京') !== -1) return '🇯🇵';
		if (txt.indexOf('us') !== -1 || txt.indexOf('美') !== -1 || txt.indexOf('gia') !== -1 || txt.indexOf('cc') !== -1) return '🇺🇸';
		if (txt.indexOf('de') !== -1 || txt.indexOf('德') !== -1 || txt.indexOf('germany') !== -1) return '🇩🇪';
		if (txt.indexOf('sg') !== -1 || txt.indexOf('新') !== -1 || txt.indexOf('singapore') !== -1) return '🇸🇬';
		if (txt.indexOf('kr') !== -1 || txt.indexOf('韩') !== -1) return '🇰🇷';
		if (txt.indexOf('uk') !== -1 || txt.indexOf('英') !== -1) return '🇬🇧';
		return '🌐';
	},

	createPipelineStep: function(tier, title, desc, action, color) {
		return E('div', { 'style': 'display: flex; align-items: center; justify-content: space-between; padding: 10px 14px; border-radius: 8px; background: #f8fafc; border-left: 4px solid ' + color + '; gap: 12px; flex-wrap: wrap;' }, [
			E('div', { 'style': 'min-width: 140px;' }, [
				E('span', { 'style': 'font-size: 11px; font-weight: 700; color: ' + color + '; display: block;' }, tier),
				E('span', { 'style': 'font-size: 13px; font-weight: 700; color: #1e293b;' }, title)
			]),
			E('div', { 'style': 'flex: 1; font-size: 12px; color: #64748b; min-width: 200px;' }, desc),
			E('div', {}, [
				E('span', { 'style': 'background: ' + color + '15; color: ' + color + '; padding: 3px 10px; border-radius: 12px; font-size: 12px; font-weight: 700;' }, action)
			])
		]);
	},

	applyAccessLogs: function(res) {
		var tbody = document.getElementById('oa-access-log-body');
		var badge = document.getElementById('oa-access-log-badge');
		if (!tbody) return;
		var records = (res && res.records) ? res.records : (Array.isArray(res) ? res : []);
		if (records.length === 0) return;
		if (badge) badge.innerText = '● 实时监听中 (' + records.length + ' 条)';
		tbody.innerHTML = '';
		records.forEach(function(rec) {
			var actionBg = '#e0f2fe', actionFg = '#0369a1', actionText = '代理 PROXY';
			if (rec.action === 'direct') {
				actionBg = '#dcfce7'; actionFg = '#15803d'; actionText = '直连 DIRECT';
			} else if (rec.action === 'dns') {
				actionBg = '#f1f5f9'; actionFg = '#475569'; actionText = '解析 DNS';
			} else if (rec.action === 'reject') {
				actionBg = '#fee2e2'; actionFg = '#b91c1c'; actionText = '拦截 REJECT';
			}
			var tr = E('tr', { 'style': 'border-bottom: 1px solid #f1f5f9;' }, [
				E('td', { 'style': 'padding: 8px 12px; color: #64748b; font-family: monospace; font-size: 12px;' }, rec.time || '-'),
				E('td', { 'style': 'padding: 8px 12px; font-weight: 600; color: #1e293b; font-family: monospace;' }, rec.target),
				E('td', { 'style': 'padding: 8px 12px; color: #334155;' }, rec.outbound_label || rec.outbound),
				E('td', { 'style': 'padding: 8px 12px; color: #64748b; font-size: 12px;' }, rec.duration || '-'),
				E('td', { 'style': 'padding: 8px 12px; text-align: center;' }, [
					E('span', { 'style': 'background: ' + actionBg + '; color: ' + actionFg + '; padding: 2px 8px; border-radius: 12px; font-size: 11px; font-weight: 700;' }, actionText)
				])
			]);
			tbody.appendChild(tr);
		});
	},

	fetchAccessLogs: function() {
		var self = this;
		var tbody = document.getElementById('oa-access-log-body');
		if (!tbody) return;
		self.apiGet('/api/access_logs').then(function(res) {
			self.applyAccessLogs(res);
		}).catch(function() {});
	},

	applyTraffic: function(t) {
		if (!t) return;
		var rxVal = document.getElementById('oa-traffic-rx-val');
		var rxSub = document.getElementById('oa-traffic-rx-sub');
		var txVal = document.getElementById('oa-traffic-tx-val');
		var txSub = document.getElementById('oa-traffic-tx-sub');
		var connVal = document.getElementById('oa-traffic-conn-val');
		var connSub = document.getElementById('oa-traffic-conn-sub');
		if (rxVal) rxVal.textContent = t.speed_rx_human || '0 B/s';
		if (rxSub) rxSub.textContent = '总接收: ' + (t.total_rx_human || '0 B');
		if (txVal) txVal.textContent = t.speed_tx_human || '0 B/s';
		if (txSub) txSub.textContent = '总发送: ' + (t.total_tx_human || '0 B');
		if (connVal) connVal.textContent = (t.active_conns || 0) + ' 个';
		if (connSub) connSub.textContent = '实时活跃连接数';
	},

	fetchTraffic: function() {
		var self = this;
		self.apiGet('/api/traffic').then(function(t) {
			self.applyTraffic(t);
		}).catch(function() {});
	},

	// Active Connections (OA-SPEC-CONN-03)
	isConnOpen: false,
	connTimer: null,
	connectionsCache: [],
	toggleConnectionsDrawer: function() {
		var drawer = document.getElementById('oa-conn-drawer');
		var btn = document.getElementById('btn-toggle-conn');
		if (!drawer) return;
		this.isConnOpen = !this.isConnOpen;
		if (this.isConnOpen) {
			drawer.style.display = 'block';
			if (btn) btn.innerText = '🔼 收起连接透视';
			this.fetchConnections();
			this.startConnectionsPolling();
		} else {
			drawer.style.display = 'none';
			if (btn) btn.innerText = '👁️ 展开实时透视';
			this.stopConnectionsPolling();
		}
	},

	startConnectionsPolling: function() {
		var self = this;
		self.stopConnectionsPolling();
		self.connTimer = setInterval(function() {
			if (self.activeTab === 'status' && self.isConnOpen) {
				self.fetchConnections();
			} else {
				self.stopConnectionsPolling();
			}
		}, 2000);
	},

	stopConnectionsPolling: function() {
		if (this.connTimer) {
			clearInterval(this.connTimer);
			this.connTimer = null;
		}
	},

	fetchConnections: function() {
		var self = this;
		var tbody = document.getElementById('oa-conn-tbody');
		var badge = document.getElementById('oa-conn-badge');
		if (!tbody || !self.isConnOpen) return;
		self.apiGet('/api/connections').then(function(res) {
			if (!res || !res.connections) return;
			self.connectionsCache = res.connections;
			if (badge) {
				var totalConns = (res.connections_count !== undefined) ? res.connections_count : (res.total || res.connections.length);
				badge.innerText = 'Top ' + res.connections.length + ' 活跃流 (总并发: ' + totalConns + ')';
			}
			var search = (document.getElementById('oa-conn-search') || {}).value || '';
			self.renderConnectionsTable(res.connections, search);
		}).catch(function() {});
	},

	filterConnections: function(search) {
		this.renderConnectionsTable(this.connectionsCache, search);
	},

	renderConnectionsTable: function(conns, search) {
		var self = this;
		var tbody = document.getElementById('oa-conn-tbody');
		if (!tbody) return;
		var q = (search || '').trim().toLowerCase();
		var filtered = conns;
		if (q) {
			filtered = conns.filter(function(c) {
				var target = (c.host || c.destination || '').toLowerCase();
				var client = (c.client_ip || (c.metadata && c.metadata.sourceIP) || '').toLowerCase();
				var name = (c.client_name || '').toLowerCase();
				var rule = (c.rule || '').toLowerCase();
				return target.indexOf(q) !== -1 || client.indexOf(q) !== -1 || name.indexOf(q) !== -1 || rule.indexOf(q) !== -1;
			});
		}
		if (filtered.length === 0) {
			tbody.innerHTML = '<tr><td colspan="7" style="text-align: center; color: #94a3b8; padding: 20px;">' + (q ? '未检索到符合条件的网络连接' : '当前暂无活跃代理连接') + '</td></tr>';
			return;
		}
		tbody.innerHTML = '';
		filtered.forEach(function(c) {
			var clientIp = c.client_ip || (c.metadata && c.metadata.sourceIP) || '-';
			var target = c.host ? (c.host + (c.destination_port ? (':' + c.destination_port) : '')) : (c.destination || '-');
			var chain = (c.rule ? ('[' + c.rule + '] ') : '') + (c.chains && c.chains.length ? c.chains[0] : (c.outbound || '-'));
			var speedUp = c.upload_speed || '0 B/s';
			var speedDown = c.download_speed || '0 B/s';
			var totalUp = c.upload_total || '0 B';
			var totalDown = c.download_total || '0 B';
			var duration = c.duration || '-';
			var clientCell = E('div', {}, [
				E('div', { 'style': 'font-weight: 600;' }, clientIp),
				c.client_name ? E('span', { 'style': 'font-size: 11px; color: #3b82f6; background: #eff6ff; padding: 1px 6px; border-radius: 4px;' }, c.client_name) : ''
			]);
			var tr = E('tr', { 'style': 'border-bottom: 1px solid #f1f5f9;' }, [
				E('td', { 'style': 'padding: 8px 12px; font-family: monospace; font-size: 12px; color: #1e293b;' }, [clientCell]),
				E('td', { 'style': 'padding: 8px 12px; font-family: monospace; font-size: 12px; color: #0f172a; word-break: break-all; max-width: 240px;' }, target),
				E('td', { 'style': 'padding: 8px 12px; font-size: 12px; color: #475569;' }, chain),
				E('td', { 'style': 'padding: 8px 12px; font-family: monospace; font-size: 12px;' }, [
					E('div', { 'style': 'color: #10b981;' }, '↓ ' + speedDown),
					E('div', { 'style': 'color: #3b82f6;' }, '↑ ' + speedUp)
				]),
				E('td', { 'style': 'padding: 8px 12px; font-family: monospace; font-size: 12px; color: #64748b;' }, [
					E('div', {}, '↓ ' + totalDown),
					E('div', {}, '↑ ' + totalUp)
				]),
				E('td', { 'style': 'padding: 8px 12px; font-size: 12px; color: #64748b;' }, duration),
				E('td', { 'style': 'padding: 8px 12px; text-align: center;' }, [
					E('button', {
						'class': 'btn btn-sm',
						'style': 'padding: 2px 8px; font-size: 11px; color: #ef4444; border: 1px solid #fee2e2; background: #fff; cursor: pointer;',
						'click': function() { self.closeConnection(c.id); }
					}, '❌ 强断')
				])
			]);
			tbody.appendChild(tr);
		});
	},

	closeConnection: function(connId) {
		var self = this;
		self.apiPost('/api/connection/close', { id: connId })
			.then(function() {
				ui.addNotification(null, E('p', {}, '✅ 已瞬时切断指定连接'), 'success');
				self.fetchConnections();
			})
			.catch(function(err) {
				ui.addNotification(null, E('p', {}, '❌ 切断连接失败: ' + err.message), 'error');
			});
	},

	closeAllConnections: function() {
		var self = this;
		if (!confirm('确定要强断所有当前活跃连接吗？此操作将瞬时重置所有代理会话。')) {
			return;
		}
		ui.showIndicator();
		self.apiPost('/api/connections/close_all', {})
			.then(function() {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '💥 已强断所有当前代理连接'), 'success');
				self.fetchConnections();
			})
			.catch(function(err) {
				ui.hideIndicator();
				ui.addNotification(null, E('p', {}, '❌ 强断失败: ' + err.message), 'error');
			});
	},

	// LAN Devices Access Control (OA-SPEC-ACL-01)
	lanDevicesCache: [],
	fetchLanDevices: function() {
		var self = this;
		var tbody = document.getElementById('oa-lan-devices-tbody');
		if (!tbody) return;
		self.apiGet('/api/lan/devices').then(function(res) {
			if (!res) return;
			if (res.bypass_ips) {
				var textareaIps = document.getElementById('oa-acl-manual-ips');
				if (textareaIps && !textareaIps.matches(':focus')) {
					textareaIps.value = res.bypass_ips.join('\n');
				}
			}
			if (res.bypass_macs) {
				var textareaMacs = document.getElementById('oa-acl-manual-macs');
				if (textareaMacs && !textareaMacs.matches(':focus')) {
					textareaMacs.value = res.bypass_macs.join('\n');
				}
			}
			if (res.devices) {
				self.lanDevicesCache = res.devices;
				self.renderLanDevicesTable(res.devices);
			}
		}).catch(function() {});
	},

	renderLanDevicesTable: function(devices) {
		var self = this;
		var tbody = document.getElementById('oa-lan-devices-tbody');
		if (!tbody) return;
		if (devices.length === 0) {
			tbody.innerHTML = '<tr><td colspan="5" style="text-align: center; color: #94a3b8; padding: 20px;">未发现局域网在线设备（检查 DHCP 租约与 ARP 缓存）</td></tr>';
			return;
		}
		tbody.innerHTML = '';
		devices.forEach(function(dev) {
			var isBypass = dev.is_bypassed || dev.is_bypass;
			var statusBadge = isBypass ?
				E('span', { 'style': 'background: #dcfce7; color: #15803d; padding: 3px 10px; border-radius: 12px; font-size: 12px; font-weight: 700;' }, '⚡ 直连免代理') :
				E('span', { 'style': 'background: #eff6ff; color: #1d4ed8; padding: 3px 10px; border-radius: 12px; font-size: 12px; font-weight: 700;' }, '🌐 智能分流代理');
			var btnText = isBypass ? '恢复智能分流' : '设为直连免代理';
			var btnClass = isBypass ? 'btn btn-sm' : 'btn btn-sm cbi-button-action';
			var tr = E('tr', { 'style': 'border-bottom: 1px solid #f1f5f9;' }, [
				E('td', { 'style': 'padding: 10px 14px; font-weight: 600; color: #1e293b;' }, [
					E('span', { 'style': 'margin-right: 6px;' }, isBypass ? '📺' : '💻'),
					dev.name || '未知设备'
				]),
				E('td', { 'style': 'padding: 10px 14px; font-family: monospace; font-size: 13px; color: #334155;' }, dev.ip),
				E('td', { 'style': 'padding: 10px 14px; font-family: monospace; font-size: 12px; color: #64748b;' }, dev.mac || '-'),
				E('td', { 'style': 'padding: 10px 14px;' }, statusBadge),
				E('td', { 'style': 'padding: 10px 14px; text-align: center;' }, [
					E('button', {
						'class': btnClass,
						'style': 'padding: 4px 14px; font-weight: 600; cursor: pointer;',
						'click': function() { self.toggleDeviceBypass(dev.ip, dev.mac, !isBypass); }
					}, btnText)
				])
			]);
			tbody.appendChild(tr);
		});
	},

	toggleDeviceBypass: function(ip, mac, setBypass) {
		var self = this;
		var textareaIps = document.getElementById('oa-acl-manual-ips');
		var textareaMacs = document.getElementById('oa-acl-manual-macs');
		var currentIps = textareaIps ? textareaIps.value.split('\n').map(function(s) { return s.trim(); }).filter(Boolean) : [];
		var currentMacs = textareaMacs ? textareaMacs.value.split('\n').map(function(s) { return s.trim().toLowerCase(); }).filter(Boolean) : [];
		ip = (ip || '').trim();
		mac = (mac || '').trim().toLowerCase();
		if (setBypass) {
			if (ip && currentIps.indexOf(ip) === -1) currentIps.push(ip);
			if (mac && currentMacs.indexOf(mac) === -1) currentMacs.push(mac);
		} else {
			if (ip) currentIps = currentIps.filter(function(x) { return x !== ip; });
			if (mac) currentMacs = currentMacs.filter(function(x) { return x.toLowerCase() !== mac; });
		}
		if (textareaIps) textareaIps.value = currentIps.join('\n');
		if (textareaMacs) textareaMacs.value = currentMacs.join('\n');
		self.saveAclSettings(currentIps, currentMacs);
	},

	saveAclSettings: function(overrideIps, overrideMacs) {
		var self = this;
		var enabled = document.getElementById('oa-acl-enabled-switch') ? document.getElementById('oa-acl-enabled-switch').checked : true;
		var textareaIps = document.getElementById('oa-acl-manual-ips');
		var textareaMacs = document.getElementById('oa-acl-manual-macs');
		var ips = overrideIps || (textareaIps ? textareaIps.value.split('\n').map(function(s) { return s.trim(); }).filter(Boolean) : []);
		var macs = overrideMacs || (textareaMacs ? textareaMacs.value.split('\n').map(function(s) { return s.trim().toLowerCase(); }).filter(Boolean) : []);
		ui.showIndicator();
		self.apiPost('/api/acl/update', {
			enabled: enabled,
			mode: 'blacklist',
			bypass_ips: ips,
			bypass_macs: macs
		}).then(function(res) {
			ui.hideIndicator();
			var timeStr = (res && res.time_ms !== undefined) ? (' (' + res.time_ms + 'ms)') : '';
			ui.addNotification(null, E('p', {}, '✅ 局域网设备分流规则 (IP/MAC) 已秒级热生效' + timeStr + '！无需重启断网。'), 'success');
			self.fetchLanDevices();
		}).catch(function(err) {
			ui.hideIndicator();
			ui.addNotification(null, E('p', {}, '❌ 保存 ACL 失败: ' + err.message), 'error');
		});
	},

	runAclDiagnosis: function() {
		var self = this;
		var clientInp = document.getElementById('oa-diag-acl-client-ip');
		var targetInp = document.getElementById('oa-diag-acl-target');
		var resBox = document.getElementById('oa-diag-acl-result');
		var btn = document.getElementById('btn-diag-acl-run');
		var clientIp = clientInp ? clientInp.value.trim() : '';
		var target = targetInp ? targetInp.value.trim() : '';
		if (!target) {
			ui.addNotification(null, E('p', {}, '请输入需要透视诊断的目标域名或 IP！'), 'warning');
			return;
		}
		if (btn) btn.innerText = '⏳ 正在透视链路中...';
		if (resBox) {
			resBox.style.display = 'block';
			resBox.innerHTML = '<div style="color: #64748b; font-size: 13px;">正在模拟客户端 ' + (clientIp || '本机') + ' 的规则树匹配...</div>';
		}
		self.apiPost('/api/diagnose_route', { target: target, client_ip: clientIp })
			.then(function(res) {
				if (btn) btn.innerText = '🚀 执行客户端链路透视诊断';
				if (!resBox) return;
				resBox.innerHTML = '';
				var statusColor = '#10b981';
				if (res.status && (res.status.indexOf('超时') !== -1 || res.status.indexOf('失败') !== -1)) {
					statusColor = '#ef4444';
				} else if (res.outbound_type === 'block') {
					statusColor = '#f59e0b';
				}
				var headerRow = E('div', { 'style': 'display: flex; gap: 8px; margin-bottom: 10px; flex-wrap: wrap; align-items: center;' }, [
					E('span', { 'style': 'font-weight: 700; font-size: 13px; color: #1e293b;' }, '诊断结果: ' + res.target),
					clientIp ? E('span', { 'style': 'background: #e0f2fe; color: #0369a1; padding: 2px 8px; border-radius: 6px; font-size: 12px;' }, '客户端: ' + clientIp) : '',
					E('span', { 'style': 'background: #e2e8f0; color: #334155; padding: 2px 8px; border-radius: 6px; font-size: 12px; font-family: monospace;' }, 'IP: ' + res.resolved_ip),
					E('span', { 'style': 'background: ' + statusColor + '20; color: ' + statusColor + '; padding: 2px 8px; border-radius: 6px; font-size: 12px; font-weight: 700;' }, res.status)
				]);
				resBox.appendChild(headerRow);
				var traceList = E('div', { 'style': 'display: flex; flex-direction: column; gap: 6px;' });
				(res.pipeline_trace || []).forEach(function(step, idx) {
					var item = E('div', { 'style': 'display: flex; align-items: center; gap: 8px; font-size: 12px; color: #334155;' }, [
						E('span', { 'style': 'width: 20px; height: 20px; border-radius: 50%; background: #3b82f6; color: #fff; display: inline-flex; align-items: center; justify-content: center; font-size: 10px; font-weight: 700; flex-shrink: 0;' }, '' + (idx + 1)),
						E('span', { 'style': 'flex: 1;' }, step)
					]);
					traceList.appendChild(item);
				});
				resBox.appendChild(traceList);
			})
			.catch(function(err) {
				if (btn) btn.innerText = '🚀 执行客户端链路透视诊断';
				if (resBox) {
					resBox.style.display = 'block';
					resBox.innerHTML = '<div style="color: #ef4444; font-size: 13px;">❌ 诊断失败: ' + err.message + '</div>';
				}
			});
	},

	eventSource: null,

	startPolling: function() {
		var self = this;
		if (self.statusTimer) {
			clearInterval(self.statusTimer);
			self.statusTimer = null;
		}
		if (self.eventSource) {
			self.eventSource.close();
			self.eventSource = null;
		}

		// Initial load
		self.fetchLogs();
		self.fetchTraffic();

		var fallbackPolling = function() {
			if (self.statusTimer) return;
			self.statusTimer = setInterval(function() {
				if (self.activeTab === 'status') {
					self.refreshStatus();
					self.fetchLogs();
					self.fetchAccessLogs();
					self.fetchTraffic();
				}
			}, 3000);
		};

		if (typeof window.EventSource !== 'undefined') {
			try {
				var es = new EventSource('/openaero-api/api/stream');
				self.eventSource = es;
				es.onmessage = function(e) {
					if (!e || !e.data) return;
					try {
						var data = JSON.parse(e.data);
						if (self.activeTab === 'status') {
							if (data.status) self.applyStatus(data.status);
							if (data.traffic) self.applyTraffic(data.traffic);
							if (data.logs) self.applyLogs(data.logs);
							if (data.access_logs) self.applyAccessLogs(data.access_logs);
						}
					} catch (parseErr) {}
				};
				es.onerror = function() {
					if (self.eventSource) {
						self.eventSource.close();
						self.eventSource = null;
					}
					fallbackPolling();
				};
			} catch (esErr) {
				fallbackPolling();
			}
		} else {
			fallbackPolling();
		}
	}
});
