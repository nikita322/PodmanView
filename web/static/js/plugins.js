// Plugins management
const PluginsManager = {
    plugins: [],
    initialized: false,
    readyPollTimer: null,

    async init() {
        if (this.initialized) return;
        this.initialized = true;

        this.bindEvents();
        await this.loadPlugins();
    },

    bindEvents() {
        const refreshBtn = document.getElementById('refresh-plugins');
        if (refreshBtn) {
            refreshBtn.addEventListener('click', () => this.loadPlugins());
        }
    },

    async loadPlugins() {
        const tbody = document.getElementById('plugins-list');

        try {
            const response = await fetch('/api/plugins', {
                headers: {
                    'Authorization': 'Bearer ' + localStorage.getItem('token')
                }
            });

            if (!response.ok) {
                throw new Error(`Failed to load plugins: ${response.status}`);
            }

            this.plugins = await response.json();
            this.renderPlugins();
        } catch (error) {
            console.error('Error loading plugins:', error);
            this.showError('Failed to load plugins');

            if (tbody) {
                tbody.innerHTML = `<tr><td colspan="5" style="text-align: center; color: red;">Error: ${error.message}</td></tr>`;
            }
        }
    },

    renderPlugins() {
        const tbody = document.getElementById('plugins-list');
        if (!tbody) return;

        if (this.plugins.length === 0) {
            tbody.innerHTML = '<tr><td colspan="5" style="text-align: center;">No plugins available</td></tr>';
            return;
        }

        tbody.querySelectorAll('tr:not([data-plugin])').forEach(row => row.remove());

        this.plugins.forEach(plugin => {
            let row = tbody.querySelector(`tr[data-plugin="${plugin.name}"]`);

            if (!row) {
                // Create new row
                row = document.createElement('tr');
                row.dataset.plugin = plugin.name;
                tbody.appendChild(row);
            }

            // Update row content
            row.innerHTML = this.rowHtml(plugin);

            // Bind toggle event for this row
            const toggle = row.querySelector('.plugin-toggle');
            if (toggle) {
                toggle.addEventListener('change', (e) => {
                    const pluginName = e.target.dataset.plugin;
                    const enabled = e.target.checked;
                    this.togglePlugin(pluginName, enabled);
                });
            }

            // Bind settings button
            const settingsBtn = row.querySelector('.plugin-settings-btn');
            if (settingsBtn) {
                settingsBtn.addEventListener('click', async (e) => {
                    const btn = e.target;
                    const pluginName = btn.dataset.plugin;
                    const originalText = btn.textContent;

                    btn.disabled = true;
                    btn.textContent = 'Loading...';

                    await this.openPluginSettings(pluginName);

                    btn.disabled = false;
                    btn.textContent = originalText;
                });
            }
        });

        // While some plugin is missing a host dependency, re-check periodically so
        // the toggle becomes available as soon as the dependency is installed.
        this.watchNotReadyPlugins();

        // Remove rows for plugins that no longer exist
        const existingRows = tbody.querySelectorAll('tr[data-plugin]');
        existingRows.forEach(row => {
            const pluginName = row.dataset.plugin;
            if (!this.plugins.find(p => p.name === pluginName)) {
                row.remove();
            }
        });
    },

    // watchNotReadyPlugins polls the plugin list (slowly) while any plugin
    // reports missing prerequisites.
    watchNotReadyPlugins() {
        const hasNotReady = this.plugins.some(plugin => plugin.ready === false);

        if (hasNotReady && !this.readyPollTimer) {
            this.readyPollTimer = setInterval(() => {
                const page = document.getElementById('page-plugins');
                if (!page || page.classList.contains('hidden')) {
                    clearInterval(this.readyPollTimer);
                    this.readyPollTimer = null;
                    return;
                }
                this.loadPlugins();
            }, 15000);
        } else if (!hasNotReady && this.readyPollTimer) {
            clearInterval(this.readyPollTimer);
            this.readyPollTimer = null;
        }
    },

    async togglePlugin(name, enabled) {
        try {
            const response = await fetch(`/api/plugins/${name}/toggle`, {
                method: 'POST',
                headers: {
                    'Authorization': 'Bearer ' + localStorage.getItem('token'),
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ enabled })
            });

            if (!response.ok) {
                // The server explains why a plugin cannot be enabled (for example
                // a missing host dependency) - surface that message verbatim.
                let message = `Failed to toggle plugin`;
                let code = '';
                try {
                    const payload = await response.json();
                    if (payload && payload.error) message = payload.error;
                    code = (payload && payload.code) || '';
                } catch (e) { /* not a JSON error body */ }
                const error = new Error(message);
                error.code = code;
                throw error;
            }

            const result = await response.json();

            // Update plugin state locally
            const plugin = this.plugins.find(p => p.name === name);
            if (plugin) {
                plugin.enabled = result.enabled;
            }

            // Update only this plugin's row in the table
            this.updatePluginRow(name);

            // Remove plugin HTML if disabled
            if (!enabled) {
                const pluginPage = document.getElementById(`page-plugin-${name}`);
                if (pluginPage) {
                    pluginPage.remove();
                }
            }

            // Show appropriate message
            let message = `Plugin "${name}" ${enabled ? 'enabled' : 'disabled'}`;
            if (result.restart_required) {
                message += '. Restart required to take effect.';
                this.showRestartWarning();
            } else {
                message += ' successfully!';
            }

            if (typeof App !== 'undefined' && App.showToast) {
                App.showToast(message, 'success');
            }
        } catch (error) {
            console.error('Error toggling plugin:', error);
            this.showError(error.message || 'Failed to toggle plugin');
            await this.loadPlugins();
        }
    },

    // escapeHtml makes plugin metadata safe to inject into innerHTML.
    escapeHtml(value) {
        return String(value == null ? '' : value)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;');
    },

    // rowHtml renders one row of the plugin table. A plugin whose host
    // prerequisites are missing cannot be enabled, so the toggle is disabled
    // and the reason is shown inline.
    rowHtml(plugin) {
        const name = this.escapeHtml(plugin.name);
        const ready = plugin.ready !== false;
        const reason = ready ? '' : (plugin.not_ready_reason || 'Missing host prerequisites');
        const toggleDisabled = (!plugin.enabled && !ready) ? 'disabled' : '';
        const titleAttr = ready ? '' : ` title="${this.escapeHtml(reason)}"`;

        return `
            <td>
                <strong>${name}</strong>
            </td>
            <td>${this.escapeHtml(plugin.description) || '-'}</td>
            <td>
                <span class="badge">${this.escapeHtml(plugin.version) || '-'}</span>
            </td>
            <td>
                <label class="toggle-label" style="margin: 0;"${titleAttr}>
                    <input type="checkbox"
                           class="plugin-toggle"
                           data-plugin="${name}"
                           ${plugin.enabled ? 'checked' : ''}
                           ${toggleDisabled}>
                    <span class="toggle-slider"></span>
                    <span class="toggle-text">${plugin.enabled ? 'Enabled' : 'Disabled'}</span>
                </label>
                ${ready ? '' : `<div style="font-size: 0.78em; color: var(--warning); margin-top: 4px; max-width: 260px;">${this.escapeHtml(reason)}</div>`}
            </td>
            <td>
                ${plugin.enabled ? `
                    <button class="btn btn-sm btn-primary plugin-settings-btn"
                            data-plugin="${name}">
                        Settings
                    </button>
                ` : ''}
            </td>
        `;
    },

    updatePluginRow(name) {
        const plugin = this.plugins.find(p => p.name === name);
        if (!plugin) return;

        const tbody = document.getElementById('plugins-list');
        const row = tbody?.querySelector(`tr[data-plugin="${name}"]`);
        if (!row) return;

        row.innerHTML = this.rowHtml(plugin);

        // Re-bind events for this row
        const toggle = row.querySelector('.plugin-toggle');
        if (toggle) {
            toggle.addEventListener('change', (e) => {
                this.togglePlugin(e.target.dataset.plugin, e.target.checked);
            });
        }

        const settingsBtn = row.querySelector('.plugin-settings-btn');
        if (settingsBtn) {
            settingsBtn.addEventListener('click', async (e) => {
                const btn = e.target;
                const pluginName = btn.dataset.plugin;
                const originalText = btn.textContent;

                btn.disabled = true;
                btn.textContent = 'Loading...';

                await this.openPluginSettings(pluginName);

                btn.disabled = false;
                btn.textContent = originalText;
            });
        }
    },

    async loadPluginHTML(name) {
        try {
            // Remove old version if exists
            const existingPage = document.getElementById(`page-plugin-${name}`);
            if (existingPage) {
                existingPage.remove();
            }

            // Add cache-busting timestamp
            const timestamp = new Date().getTime();
            const response = await fetch(`/api/plugins/${name}/html?v=${timestamp}`, {
                headers: {
                    'Authorization': 'Bearer ' + localStorage.getItem('token'),
                    'Cache-Control': 'no-cache, no-store, must-revalidate',
                    'Pragma': 'no-cache'
                }
            });

            if (!response.ok) {
                return false;
            }

            const html = await response.text();
            const mainContent = document.querySelector('.main-content');
            if (mainContent) {
                const tempDiv = document.createElement('div');
                tempDiv.innerHTML = html;

                // Append all children (section + scripts), not just first element
                while (tempDiv.firstChild) {
                    const node = tempDiv.firstChild;
                    tempDiv.removeChild(node);
                    mainContent.appendChild(node);

                    // If it's a script, recreate it to trigger execution
                    if (node.tagName === 'SCRIPT') {
                        const newScript = document.createElement('script');
                        if (node.src) {
                            newScript.src = node.src;
                        } else {
                            newScript.textContent = node.textContent;
                        }
                        node.parentNode.replaceChild(newScript, node);
                    } else if (node.nodeType === 1) {
                        // For elements, recreate inline scripts within them
                        const scripts = node.querySelectorAll('script');
                        scripts.forEach(oldScript => {
                            const newScript = document.createElement('script');
                            if (oldScript.src) {
                                newScript.src = oldScript.src;
                            } else {
                                newScript.textContent = oldScript.textContent;
                            }
                            oldScript.parentNode.replaceChild(newScript, oldScript);
                        });
                    }
                }
            }
            return true;
        } catch (error) {
            console.error(`Failed to load HTML for plugin ${name}:`, error);
            return false;
        }
    },

    async openPluginSettings(name) {
        const loaded = await this.loadPluginHTML(name);

        if (!loaded) {
            if (typeof App !== 'undefined' && App.showToast) {
                App.showToast(`Plugin "${name}" has no settings page`, 'info');
            }
            return;
        }

        const pluginPage = document.getElementById(`page-plugin-${name}`);
        if (pluginPage) {
            document.querySelectorAll('.content-page').forEach(page => {
                page.classList.add('hidden');
            });
            pluginPage.classList.remove('hidden');

            document.querySelectorAll('.nav-item').forEach(item => {
                item.classList.remove('active');
            });

            // Trigger plugin initialization after showing the page
            const initEvent = new CustomEvent('plugin-page-shown', { detail: { name } });
            pluginPage.dispatchEvent(initEvent);
        }
    },

    showRestartWarning() {
        const warning = document.createElement('div');
        warning.className = 'plugin-restart-warning';
        warning.innerHTML = `
            <strong>Restart Required</strong>
            <p>Changes will take effect after server restart.</p>
        `;
        document.body.appendChild(warning);

        setTimeout(() => warning.remove(), 10000);
    },

    showError(message) {
        if (typeof App !== 'undefined' && App.showToast) {
            App.showToast(message, 'error');
        } else {
            alert(message);
        }
    }
};

document.addEventListener('DOMContentLoaded', function() {
    const pluginsPage = document.getElementById('page-plugins');
    if (!pluginsPage) return;

    const observer = new MutationObserver(function(mutations) {
        mutations.forEach(function(mutation) {
            if (mutation.target.id === 'page-plugins' &&
                !mutation.target.classList.contains('hidden')) {
                PluginsManager.init();
            }
        });
    });

    observer.observe(pluginsPage, {
        attributes: true,
        attributeFilter: ['class']
    });

    if (!pluginsPage.classList.contains('hidden')) {
        PluginsManager.init();
    }
});
