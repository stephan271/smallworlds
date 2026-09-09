/* Procedural app dioramas. Three.js is pinned and served locally; no API data,
 * credentials, remote textures or app services are needed by this preview. */
const el = (id) => document.getElementById(id);
const host = el('world-preview');
const catalog = {
  nextcloud: ['#8fcaa2', 'Files · Nextcloud', 'A shared archive: giant folders, storage halls and delivery vans.'],
  immich: ['#e8b894', 'Photos · Immich', 'A photo garden: a camera pavilion and an open-air picture gallery.'],
  forgejo: ['#ddaa75', 'Git · Forgejo', 'A software workshop: branches connect glowing commit stations.'],
  excalidraw: ['#b9a6d6', 'Whiteboard · Excalidraw', 'A creative studio: an oversized pencil and a shared drawing easel.'],
  jitsi: ['#8ccbc7', 'Meet · Jitsi', 'A meeting plaza: video screens face a communal round table.'],
  collabora: ['#a8c680', 'Collabora', 'A document campus: pages, a spreadsheet and an editing desk.'],
  'euro-office': ['#88b4db', 'Euro-Office', 'A European office campus: documents, charts and a shared editing desk.'],
  plane: ['#b6addf', 'Plan · Plane', 'A project square: a three-column task board tracks work across the town.'],
  stalwart: ['#e3b879', 'Mail · Stalwart', 'A post office: a giant envelope, sorting depot and postal delivery vans.'],
  bulwark: ['#cf9dbb', 'Webmail · Bulwark', 'An inbox café: an open mailbox and a screen for reading letters.'],
  'pod-gateway': ['#8dbda8', 'Personal data pods', 'A neighbourhood of private homes, each with its own personal data vault.'],
};
let started = false;
new IntersectionObserver(async (entries, observer) => {
  if (!entries.some((entry) => entry.isIntersecting) || started) return;
  started = true;
  observer.disconnect();
  try {
    const THREE = await import('./vendor/three.module.min.js');
    await createWorld(THREE);
  } catch {
    el('world-fallback').hidden = false;
    el('world-fallback').textContent = 'The 3D preview is unavailable in this browser. You can still choose all your apps below.';
    host.querySelectorAll('.world-toolbar button').forEach((button) => { button.disabled = true; });
  }
}).observe(host);

async function createWorld(T) {
  let canvas = el('world-canvas');
  const viewport = el('world-viewport');
  let renderer;
  // Check the actual context before invoking Three.js: a disabled GPU is an
  // expected capability result, not a renderer exception to log to the user.
  try {
    const context = canvas.getContext('webgl2', { antialias: true, alpha: true });
    if (context) renderer = new T.WebGLRenderer({ canvas, context, alpha: true });
  } catch {
    // A context can exist even if renderer initialization fails. A fresh canvas
    // is required because an element cannot switch from WebGL to Canvas 2D.
  }
  const software = !renderer;
  if (software) {
    const replacement = canvas.cloneNode(false);
    canvas.replaceWith(replacement);
    canvas = replacement;
    const { GlobeCanvasRenderer } = await import('./globe-canvas.js');
    renderer = new GlobeCanvasRenderer(T, canvas);
  }
  host.dataset.renderer = software ? 'canvas' : 'webgl';
  el('world-rendering-note').hidden = !software;
  renderer.setPixelRatio(Math.min(devicePixelRatio, 1.75));
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = T.PCFSoftShadowMap;
  renderer.toneMapping = T.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.35;
  const scene = new T.Scene();
  const camera = new T.PerspectiveCamera(39, 1, .1, 60);
  let distance = 9.6;
  camera.position.set(0, 0, distance);
  const world = new T.Group();
  scene.add(world);
  scene.add(new T.HemisphereLight('#c8eeff', '#395262', 2.1));
  const sun = new T.DirectionalLight('#ffe1b1', 4.2);
  sun.position.set(-4, 7, 6);
  sun.castShadow = true;
  sun.shadow.mapSize.set(2048, 2048);
  Object.assign(sun.shadow.camera, { left: -4, right: 4, top: 4, bottom: -4, near: .5, far: 20 });
  sun.shadow.normalBias = .025;
  scene.add(sun);
  const rim = new T.DirectionalLight('#68bbff', 3);
  rim.position.set(4, 1, -5);
  scene.add(rim);
  const materials = new Map();
  function material(color, glowing = false) {
    const key = `${color}:${glowing}`;
    if (!materials.has(key)) materials.set(key, new T.MeshStandardMaterial({
      color, roughness: .72, metalness: .04,
      ...(glowing ? { emissive: color, emissiveIntensity: .55 } : {}),
    }));
    return materials.get(key);
  }
  function mesh(parent, geometry, color, x = 0, y = 0, z = 0, glow = false) {
    const item = new T.Mesh(geometry, material(color, glow));
    item.position.set(x, y, z);
    item.castShadow = true;
    item.receiveShadow = true;
    parent.add(item);
    return item;
  }
  function box(parent, color, x, y, z, w, h, d, glow = false) {
    return mesh(parent, new T.BoxGeometry(w, h, d), color, x, y, z, glow);
  }
  function ball(parent, color, x, y, z, radius) {
    return mesh(parent, new T.IcosahedronGeometry(radius, 1), color, x, y, z);
  }
  function cylinder(parent, color, x, y, z, radius, height) {
    return mesh(parent, new T.CylinderGeometry(radius, radius, height, 16), color, x, y, z);
  }
  function line(parent, color, points, radius = .012) {
    const curve = new T.CatmullRomCurve3(points.map((p) => new T.Vector3(...p)));
    return mesh(parent, new T.TubeGeometry(curve, 20, radius, 5, false), color);
  }
  mesh(world, new T.SphereGeometry(2.4, software ? 40 : 80, software ? 24 : 48), '#287e96');
  // Fine water facets catch the warm key light without hiding the land masses.
  const water = mesh(world, new T.IcosahedronGeometry(2.402, 4), '#4098ad');
  water.material = new T.MeshStandardMaterial({ color: '#64b8c5', transparent: true, opacity: .13, wireframe: true });
  water.castShadow = false;
  const atmosphere = new T.Mesh(new T.SphereGeometry(2.47, 48, 32), new T.ShaderMaterial({
    transparent: true, depthWrite: false, side: T.BackSide, blending: T.AdditiveBlending,
    vertexShader: 'varying vec3 n; varying vec3 v; void main(){vec4 p=modelViewMatrix*vec4(position,1.); n=normalize(normalMatrix*normal); v=normalize(-p.xyz); gl_Position=projectionMatrix*p;}',
    fragmentShader: 'varying vec3 n; varying vec3 v; void main(){float f=pow(1.-abs(dot(normalize(n),normalize(v))),3.); gl_FragColor=vec4(.25,.7,.9,f*.3);}',
  }));
  world.add(atmosphere);
  // A sparse, deterministic star field keeps the composition calm.
  const stars = [];
  for (let i = 0; i < 160; i++) {
    stars.push(Math.sin(i * 127.1) * 18, Math.cos(i * 311.7) * 13, -8 - (i % 7));
  }
  const starGeometry = new T.BufferGeometry();
  starGeometry.setAttribute('position', new T.Float32BufferAttribute(stars, 3));
  scene.add(new T.Points(starGeometry, new T.PointsMaterial({ color: '#bddbdc', size: .025, transparent: true, opacity: .6 })));

  function building(parent, x, z, color = '#f2e6cd', height = .24) {
    box(parent, color, x, height / 2, z, .22, height, .19);
    box(parent, '#486879', x, height + .02, z, .25, .04, .22);
    for (let i = 0; i < 2; i++) box(parent, '#ffd892', x - .055 + i * .11, height * .58, z + .098, .045, .065, .007, true);
  }
  function screen(parent, x, y, z, w = .6, h = .4, color = '#b1e5e6') {
    box(parent, '#365062', x, y, z, w, h, .055);
    box(parent, color, x, y, z + .032, w - .045, h - .045, .01);
    box(parent, '#c5ad8c', x, y / 2, z, .025, y, .03);
  }
  function page(parent, x, y, z, color = '#ffffff') {
    box(parent, color, x, y, z, .25, .36, .045);
    for (let i = 0; i < 4; i++) box(parent, '#789da8', x, y + .09 - i * .06, z + .025, .16, .012, .007);
  }
  function envelope(parent, x, y, z, width = .55) {
    box(parent, '#fff2d6', x, y, z, width, width * .62, .08);
    line(parent, '#cf885f', [[x - width / 2, y + width * .3, z + .045], [x, y, z + .047], [x + width / 2, y + width * .3, z + .045]], .014);
  }
  function landmark(parent, name) {
    switch (name) {
      case 'nextcloud':
        building(parent, -.27, -.18, '#e9e7da', .36);
        for (let i = 0; i < 3; i++) {
          box(parent, ['#e8bb68', '#f0cd87', '#ffdf9c'][i], -.1 + i * .13, .28 + i * .035, .04 + i * .07, .4, .43, .065);
          box(parent, '#ffdf9c', -.21 + i * .13, .51 + i * .035, .04 + i * .07, .16, .07, .065);
        }
        for (let i = 0; i < 3; i++) ball(parent, '#e1f6f7', -.2 + i * .14, .78 + (i === 1 ? .07 : 0), .02, .12);
        break;
      case 'immich': {
        box(parent, '#f6e0ca', 0, .25, -.08, .57, .36, .24);
        box(parent, '#4d6975', -.12, .46, -.08, .17, .08, .14);
        const lens = cylinder(parent, '#273f53', .05, .26, .095, .14, .13);
        lens.rotation.x = Math.PI / 2;
        const glass = cylinder(parent, '#74c9db', .05, .26, .17, .096, .02);
        glass.rotation.x = Math.PI / 2;
        for (let i = 0; i < 2; i++) {
          screen(parent, -.35 + i * .65, .21, .3, .22, .24, '#90c5d2');
          mesh(parent, new T.ConeGeometry(.085, .13, 3), '#4d977a', -.35 + i * .65, .19, .34);
          ball(parent, '#ffe0a1', -.30 + i * .65, .27, .345, .025);
        }
        break;
      }
      case 'forgejo':
        building(parent, 0, -.23, '#deb189', .27);
        line(parent, '#ffce85', [[-.28,.12,.25],[0,.15,.08],[.3,.16,.28]], .027);
        line(parent, '#ffce85', [[0,.15,.08],[.18,.25,-.12]], .027);
        [[-.28,.12,.25],[0,.15,.08],[.3,.16,.28],[.18,.25,-.12]].forEach(([x,y,z]) => ball(parent, '#e87747', x,y,z,.075));
        screen(parent, 0, .52, -.24, .45, .24, '#243d50');
        line(parent, '#a8e5c6', [[-.08,.56,-.2],[-.14,.52,-.2],[-.08,.48,-.2]]);
        line(parent, '#a8e5c6', [[.08,.56,-.2],[.14,.52,-.2],[.08,.48,-.2]]);
        break;
      case 'excalidraw': {
        screen(parent, -.06, .38, -.07, .51, .43, '#fff0db');
        line(parent, '#bc82b1', [[-.22,.28,-.03],[-.15,.46,-.03],[.05,.32,-.03],[.13,.46,-.03]], .018);
        const pencil = new T.Group(); parent.add(pencil); pencil.position.set(.32,.04,.1); pencil.rotation.z = -.22;
        mesh(pencil, new T.CylinderGeometry(.045,.045,.6,6), '#efba5f',0,.34,0);
        mesh(pencil, new T.ConeGeometry(.045,.13,6), '#354956',0,.705,0);
        cylinder(parent, '#d699b5', -.25,.055,.28,.12,.1);
        break;
      }
      case 'jitsi':
        cylinder(parent, '#efd6aa', 0,.14,.15,.24,.06);
        cylinder(parent, '#46616c', 0,.07,.15,.035,.14);
        for (let i = 0; i < 2; i++) {
          screen(parent,-.23 + i * .45,.38,-.14,.36,.32,'#83bfc4');
          ball(parent,'#ffe0b0',-.23+i*.45,.42,-.095,.06);
          box(parent,'#4a7e96',-.23+i*.45,.31,-.095,.12,.09,.015);
        }
        break;
      case 'collabora': case 'euro-office':
        box(parent, '#c09875', 0,.15,.05,.6,.06,.32);
        page(parent,-.15,.4,-.04);
        page(parent,.16,.44,-.1,'#def0dc');
        for(let i=0;i<3;i++) box(parent,'#62a78b',.09+i*.065,.43,-.068,.03,.06+i*.045,.015);
        break;
      case 'plane':
        screen(parent,0,.4,-.09,.68,.46,'#e4e8f3');
        for(let col=0;col<3;col++) for(let row=0;row<3-col;row++)
          box(parent,['#d5a25e','#ac91cf','#7cb6a0'][col],-.22+col*.22,.52-row*.1,-.052,.16,.07,.012);
        break;
      case 'stalwart':
        building(parent,0,-.13,'#d8906d',.3);
        envelope(parent,0,.5,-.06);
        box(parent,'#42687c',-.32,.14,.22,.13,.28,.14);
        box(parent,'#ffda92',-.32,.19,.295,.09,.025,.008);
        break;
      case 'bulwark':
        screen(parent,.12,.36,-.12,.46,.36,'#9fc9d6');
        envelope(parent,.12,.36,-.075,.29);
        box(parent,'#ad759c',-.3,.2,.15,.23,.25,.24);
        box(parent,'#f2dcb8',-.3,.09,.33,.23,.025,.22);
        envelope(parent,-.3,.26,.27,.18);
        break;
      case 'pod-gateway':
        for(let i=0;i<3;i++) {
          const x=-.3+i*.3, z=i===1?-.18:.12;
          building(parent,x,z,'#ecdcb7',.21);
          mesh(parent,new T.ConeGeometry(.2,.15,4),'#789e8b',x,.3,z).rotation.y=Math.PI/4;
          box(parent,'#547a93',x+.1,.08,z+.15,.1,.15,.1);
          ball(parent,'#a4efd5',x+.1,.1,z+.21,.015);
        }
        break;
      default: building(parent,0,0);
    }
  }

  const islands = new Map();
  // Fixed slots preserve spatial memory when another app is selected.
  const slots = [[-29,-31],[28,29],[29,-32],[-29,30],[0,94],[49,146],[-12,158],[-52,110],[42,-112],[-24,-103],[-52,-163]];
  function makeIsland(name) {
    const index = Object.keys(catalog).indexOf(name);
    const [color, title] = catalog[name];
    const [lat, lon] = slots[index].map(T.MathUtils.degToRad);
    const normal = new T.Vector3(Math.cos(lat)*Math.sin(lon),Math.sin(lat),Math.cos(lat)*Math.cos(lon));
    const root = new T.Group();
    root.position.copy(normal).multiplyScalar(2.42);
    root.quaternion.setFromUnitVectors(new T.Vector3(0,1,0),normal);
    world.add(root);
    const shape = new T.Shape();
    for (let i=0;i<40;i++) {
      const a=i/40*Math.PI*2;
      const r=.82*(1+.09*Math.sin(a*3+index)+.055*Math.cos(a*7+index));
      const x=Math.cos(a)*r, y=Math.sin(a)*r*.87;
      if(i===0) shape.moveTo(x,y); else shape.lineTo(x,y);
    }
    shape.closePath();
    const geo = new T.ExtrudeGeometry(shape,{depth:.25,bevelEnabled:true,bevelSegments:2,steps:1,bevelSize:.035,bevelThickness:.035});
    geo.rotateX(-Math.PI/2); geo.translate(0,-.23,0);
    const land = new T.Mesh(geo,[material(color),material('#b79773')]);
    land.castShadow=true; land.receiveShadow=true; root.add(land);
    // Closed oval road and separately orbiting pedestrians stay on the plateau.
    const road = mesh(root,new T.RingGeometry(.54,.63,64),'#59716e',0,.065,0);
    road.rotation.x=-Math.PI/2; road.scale.y=.84;
    for(let i=0;i<28;i++) {
      const a=i/28*Math.PI*2;
      const dash=box(root,'#e3d9b7',Math.cos(a)*.585,.069,Math.sin(a)*.49,.032,.005,.009);
      dash.rotation.y=-a;
    }
    for(let i=0;i<6;i++) {
      const a=i*2.4+index, x=Math.cos(a)*.7,z=Math.sin(a)*.59;
      cylinder(root,'#94775a',x,.095,z,.021,.16);
      mesh(root,new T.ConeGeometry(.095,.23,7),i%2?'#4d8a70':'#649d78',x,.24,z);
    }
    building(root,-.36,-.28,'#e8dfc8',.16);
    landmark(root,name);
    const movers=[];
    for(let i=0;i<2;i++) {
      const car=new T.Group(); root.add(car);
      box(car,i?'#f4d88e':'#e7eee9',0,.07,0,.14,.07,.075);
      box(car,'#4d7785',.005,.12,0,.065,.05,.066);
      for(const x of [-.045,.045]) for(const z of [-.04,.04]) ball(car,'#304650',x,.038,z,.021);
      box(car,'#ffe7b0',.072,.07,0,.006,.022,.05,true);
      movers.push({object:car,phase:i*Math.PI,speed:.32+i*.06,r:.585,z:.49,person:false});
    }
    for(let i=0;i<5;i++) {
      const person=new T.Group(); root.add(person);
      cylinder(person,['#c87864','#efe1b8','#4d718d'][i%3],0,.095,0,.023,.075);
      ball(person,'#ebbb92',0,.158,0,.03);
      const legs=[box(person,'#374c63',-.012,.035,0,.016,.06,.018),box(person,'#374c63',.012,.035,0,.016,.06,.018)];
      movers.push({object:person,legs,phase:i*1.26,speed:.09+i*.013,r:.465,z:.39,person:true});
    }
    const label=document.createElement('span'); label.className='world-label'; label.textContent=title.split(' · ')[0];
    el('world-labels').append(label);
    return {root,normal,label,movers,title};
  }
  function removeIsland(island) {
    world.remove(island.root);
    island.root.traverse((object)=>object.geometry?.dispose());
    island.label.remove();
  }
  let paused=matchMedia('(prefers-reduced-motion: reduce)').matches;
  let dragging=false, orbiting=true, visible=false, frame=0, last=0, elapsed=0, focused='';
  const reduced=matchMedia('(prefers-reduced-motion: reduce)');
  function motionLabel() {
    el('world-motion').textContent=paused?'Resume':'Pause';
    el('world-motion').setAttribute('aria-pressed',String(paused));
  }
  reduced.addEventListener('change',()=>{paused=reduced.matches;motionLabel();wake();});
  motionLabel();
  function focus(name) {
    const island=islands.get(name); if(!island)return;
    // Present the diorama obliquely, with its front facing the viewer.
    world.quaternion.setFromAxisAngle(new T.Vector3(1,0,0),1.0)
      .multiply(island.root.quaternion.clone().invert());
    focused=name; orbiting=false;
    el('world-detail').textContent=catalog[name][2];
    el('world-legend').querySelectorAll('button').forEach((button)=>button.setAttribute('aria-pressed',String(button.dataset.app===name)));
    wake();
  }
  function selection() {
    const selected=[...document.querySelectorAll('.app input:checked')].map((input)=>input.value).filter((name)=>catalog[name]);
    for(const [name,island] of islands) if(!selected.includes(name)){removeIsland(island);islands.delete(name);}
    for(const name of selected) if(!islands.has(name)) islands.set(name,makeIsland(name));
    el('world-summary').textContent=selected.length?`${selected.length} app continents · Identity & Dashboard connect your world`:'An ocean of possibilities. Select an app to create your first continent.';
    el('world-legend').replaceChildren();
    for(const name of selected) {
      const button=document.createElement('button');button.type='button';button.textContent=catalog[name][1];button.dataset.app=name;
      button.style.setProperty('--continent-color',catalog[name][0]);
      button.setAttribute('aria-pressed',String(focused===name));
      button.addEventListener('click',()=>focus(name));el('world-legend').append(button);
    }
    if(!selected.includes(focused)) {focused='';el('world-detail').textContent='Choose a continent to discover its neighbourhood.';}
    host.dataset.continents=selected.join(',');
    wake();
  }
  function resize() {
    const {width,height}=viewport.getBoundingClientRect();
    if(!width||!height)return;
    renderer.setSize(width,height,false);camera.aspect=width/height;camera.updateProjectionMatrix();wake();
  }
  function draw(delta) {
    if(!paused) {
      elapsed+=delta;
      if(!dragging&&orbiting)world.rotateOnWorldAxis(new T.Vector3(0,1,0),delta*.055);
    }
    camera.position.z=distance;
    world.updateMatrixWorld(true);
    for(const island of islands.values()) {
      for(const mover of island.movers) {
        const a=elapsed*mover.speed+mover.phase;
        mover.object.position.set(Math.cos(a)*mover.r,.055,Math.sin(a)*mover.z);
        mover.object.rotation.y=Math.atan2(-Math.cos(a)*mover.z,-Math.sin(a)*mover.r);
        if(mover.person) mover.legs.forEach((leg,i)=>{leg.rotation.x=Math.sin(elapsed*7+mover.phase+i*Math.PI)*.45;});
      }
      const point=island.root.localToWorld(new T.Vector3(0,.08,.83));
      const facing=island.normal.clone().applyQuaternion(world.quaternion);
      island.label.hidden=facing.dot(camera.position.clone().sub(point).normalize())<.3;
      point.project(camera);
      island.label.style.left=`${(point.x*.5+.5)*viewport.clientWidth}px`;
      island.label.style.top=`${(-point.y*.5+.5)*viewport.clientHeight}px`;
    }
    renderer.render(scene,camera);
  }
  function tick(now) {
    frame=0;
    if(!visible||document.hidden)return;
    if(software && !paused && last && now-last < 50) {
      frame=requestAnimationFrame(tick);
      return;
    }
    const delta=last?Math.min((now-last)/1000,.05):0;last=now;
    draw(delta);
    if(!paused)frame=requestAnimationFrame(tick);
  }
  function wake() {if(visible&&!document.hidden&&!frame)frame=requestAnimationFrame(tick);}
  new ResizeObserver(resize).observe(viewport);
  new IntersectionObserver((entries)=>{visible=entries[0].isIntersecting;last=0;if(!visible){cancelAnimationFrame(frame);frame=0;}else wake();}).observe(viewport);
  document.addEventListener('visibilitychange',()=>{last=0;wake();});
  new MutationObserver(selection).observe(el('app-grid'),{childList:true,subtree:true});
  el('app-grid').addEventListener('change',selection);
  el('world-motion').addEventListener('click',()=>{paused=!paused;motionLabel();wake();});
  function zoom(amount){distance=T.MathUtils.clamp(distance+amount,7.3,14);wake();}
  el('world-zoom-in').addEventListener('click',()=>zoom(-.7));
  el('world-zoom-out').addEventListener('click',()=>zoom(.7));
  el('world-reset').addEventListener('click',()=>{world.quaternion.identity();distance=9.6;focused='';orbiting=true;selection();wake();});
  function rotate(x,y) {
    orbiting=false;
    world.rotateOnWorldAxis(new T.Vector3(0,1,0),x);
    world.rotateOnWorldAxis(new T.Vector3(1,0,0),y);wake();
  }
  let pointer=null;
  canvas.addEventListener('pointerdown',(event)=>{
    if(pointer)return;
    pointer={id:event.pointerId,x:event.clientX,y:event.clientY};dragging=true;
    canvas.setPointerCapture(event.pointerId);
  });
  canvas.addEventListener('pointermove',(event)=>{
    if(!pointer||pointer.id!==event.pointerId)return;
    rotate((event.clientX-pointer.x)*.006,(event.clientY-pointer.y)*.006);
    pointer.x=event.clientX;pointer.y=event.clientY;
  });
  function release(){pointer=null;dragging=false;}
  canvas.addEventListener('pointerup',release);canvas.addEventListener('pointercancel',release);canvas.addEventListener('lostpointercapture',release);
  canvas.addEventListener('keydown',(event)=>{
    const keys={ArrowLeft:[-.12,0],ArrowRight:[.12,0],ArrowUp:[0,-.12],ArrowDown:[0,.12]};
    if(keys[event.key]){event.preventDefault();rotate(...keys[event.key]);}
    if(['+','=','-'].includes(event.key)){event.preventDefault();zoom(event.key==='-'?.5:-.5);}
  });
  canvas.addEventListener('wheel',(event)=>{if(document.activeElement!==canvas)return;event.preventDefault();zoom(Math.sign(event.deltaY)*.35);},{passive:false});
  canvas.addEventListener('webglcontextlost',(event)=>{
    event.preventDefault();cancelAnimationFrame(frame);frame=0;visible=false;
    el('world-fallback').hidden=false;
    el('world-fallback').textContent='The 3D preview was interrupted. Reload this page to restore it, or continue choosing your apps below.';
  });
  el('world-fallback').hidden=true;
  host.dataset.ready='true';
  selection();resize();
}
