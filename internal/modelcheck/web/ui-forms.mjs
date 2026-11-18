// Magpie's iframe grants scripts, not native form navigation (allow-forms).
// Use explicit click/key actions, with the same validation and duplicate guard.
export function bindFormAction(form,onAction){
  let running=false;
  const buttons=[...form.querySelectorAll('button[type="submit"]')];
  const run=async event=>{
    event.preventDefault();
    if(running||!form.reportValidity())return;
    running=true;
    const previous=buttons.map(button=>button.disabled);
    buttons.forEach(button=>{button.disabled=true;});
    try{await onAction(form);}
    finally{buttons.forEach((button,i)=>{button.disabled=previous[i];});running=false;}
  };
  form.addEventListener('submit',run);
  buttons.forEach(button=>button.addEventListener('click',run));
  form.addEventListener('keydown',event=>{
    if(event.key==='Enter'&&!event.isComposing&&event.target?.tagName==='INPUT'&&
      !['checkbox','radio','file','button','submit'].includes(event.target.type))run(event);
  });
}
