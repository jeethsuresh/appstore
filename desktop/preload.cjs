'use strict';
const { contextBridge, ipcRenderer } = require('electron');
const allowed = new Set(['state', 'refresh', 'login', 'install', 'launch', 'updateAll', 'restart', 'settings', 'logout']);
contextBridge.exposeInMainWorld('store', {
  action: (name, value) => { if (!allowed.has(name)) return Promise.reject(new Error('Invalid action.')); return ipcRenderer.invoke('store:action', name, value); },
  subscribe: callback => { const listener = (_event, state) => callback(state); ipcRenderer.on('store:state', listener); return () => ipcRenderer.removeListener('store:state', listener); },
});
