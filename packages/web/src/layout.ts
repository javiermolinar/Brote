/** Resize the workspace without replacing panel contents or their scroll state. */
export function initializePanelResizing(): void {
  const workspace = document.querySelector<HTMLElement>('.workspace')!;
  const editor = document.querySelector<HTMLElement>('.editorPane')!;
  const navigation = document.querySelector<HTMLElement>('.navigation')!;
  const discussion = document.querySelector<HTMLElement>('.inspectorPane')!;
  const dock = document.querySelector<HTMLElement>('.inspectionDock')!;
  const configs = [
    {panel:navigation, host:editor, name:'Runs and stack width', property:'--navigation-width', vertical:true, sign:1, edge:'navigationResize', min:140},
    {panel:discussion, host:editor, name:'Discussions width', property:'--discussion-width', vertical:true, sign:-1, edge:'discussionResize', min:220},
    {panel:dock, host:editor, name:'Locals and expressions height', property:'--inspection-height', vertical:false, sign:-1, edge:'inspectionResize', min:80},
  ];
  for (const config of configs) {
    const handle = document.createElement('div');
    handle.className = 'panelResize ' + config.edge;
    handle.tabIndex = 0;
    handle.setAttribute('role','separator');
    handle.setAttribute('aria-label',config.name);
    handle.setAttribute('aria-orientation',config.vertical?'vertical':'horizontal');
    handle.title = config.name + ' · drag or use arrow keys · double-click to reset';
    if (!config.panel.id) config.panel.id = config.edge + 'Panel';
    handle.setAttribute('aria-controls',config.panel.id);
    config.host.append(handle);
    const size = () => config.vertical ? config.panel.getBoundingClientRect().width : config.panel.getBoundingClientRect().height;
    const limits = () => {
      const other = config.panel === navigation ? discussion : navigation;
      const available = config.vertical
        ? workspace.clientWidth - (other.getClientRects().length ? other.getBoundingClientRect().width : 0) - 260
        : dock.getBoundingClientRect().height + document.getElementById('source')!.clientHeight - 100;
      const fraction = config.panel === navigation ? .3 : other.getClientRects().length ? .4 : .45;
      const maximum = Math.max(0,config.vertical ? Math.min(available,workspace.clientWidth*fraction) : available);
      return {min:Math.min(config.min,maximum),max:maximum};
    };
    const update = () => {
      const {min,max} = limits();
      if(!config.vertical)handle.style.bottom=size()+'px';
      handle.setAttribute('aria-valuemin',String(Math.round(min)));
      handle.setAttribute('aria-valuemax',String(Math.round(max)));
      handle.setAttribute('aria-valuenow',String(Math.round(size())));
      handle.setAttribute('aria-valuetext',Math.round(size())+' pixels');
    };
    const resize = (value:number) => {
      const {min,max} = limits();
      workspace.style.setProperty(config.property,Math.min(max,Math.max(min,value))+'px');
      update();
    };
    let drag: {id:number;position:number;size:number} | undefined;
    handle.addEventListener('pointerdown',event=>{
      if(event.button!==0)return;
      event.preventDefault();handle.focus();
      drag={id:event.pointerId,position:config.vertical?event.clientX:event.clientY,size:size()};
      handle.setPointerCapture(event.pointerId);
      document.body.classList.add(config.vertical?'resizingColumns':'resizingRows');
    });
    handle.addEventListener('pointermove',event=>{
      if(drag?.id!==event.pointerId)return;
      resize(drag.size+config.sign*((config.vertical?event.clientX:event.clientY)-drag.position));
    });
    const finish=()=>{drag=undefined;document.body.classList.remove('resizingColumns','resizingRows');};
    handle.addEventListener('pointerup',finish);
    handle.addEventListener('pointercancel',finish);
    handle.addEventListener('lostpointercapture',finish);
    const reset=()=>{workspace.style.removeProperty(config.property);update();};
    handle.addEventListener('dblclick',reset);
    handle.addEventListener('keydown',event=>{
      const direction = config.vertical ? {ArrowLeft:-1,ArrowRight:1} : {ArrowUp:-1,ArrowDown:1};
      const delta = direction[event.key as keyof typeof direction];
      if(delta){event.preventDefault();resize(size()+delta*config.sign*(event.shiftKey?50:10));}
      if(event.key==='Home'){event.preventDefault();resize(limits().min);}
      if(event.key==='End'){event.preventDefault();resize(limits().max);}
      if(event.key==='Enter'){event.preventDefault();reset();}
    });
    if(typeof ResizeObserver!=='undefined')new ResizeObserver(update).observe(config.panel);
    update();
  }
}
