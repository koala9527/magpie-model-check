(async()=>{
  await magpie.ready;
  const options=await magpie.options.get(),port=options.port??3479;
  if(!Number.isInteger(port)||port<1||port>65535){document.getElementById('loading').textContent='请在插件选项中填写有效的本机页面端口。';return;}
  // Ordinary file pages have frame-src/connect-src 'none'. Navigation to the
  // declared service is the official page-service path, not an iframe escape.
  location.replace(magpie.service||`http://127.0.0.1:${port}/`);
})().catch(()=>{document.getElementById('loading').textContent='本机检测页面尚未连接，请重新打开插件页面。';});
