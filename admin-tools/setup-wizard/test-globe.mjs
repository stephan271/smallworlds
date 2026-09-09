/* Isolated browser checks for the globe. The existing interface suite walks
 * navigation; here we reveal the app panel directly to test its visualization.
 * Run via admin-tools/test-setup-wizard.sh against the no-op wizard. */
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const software = process.argv[3] === 'software';
const browser=await chromium.launch({args:software ? ['--disable-webgl'] : ['--enable-unsafe-swiftshader']});
const page=await browser.newPage({viewport:{width:1000,height:1100}});
const errors=[];page.on('pageerror',e=>errors.push(e.message));
page.on('console', message => {
  if (message.type() === 'error' && message.text().includes('THREE.WebGLRenderer')) errors.push(message.text());
});
await page.goto(process.argv[2]);
await page.waitForSelector('.app input',{state:'attached'});
await page.evaluate(()=>{document.querySelectorAll('.panel').forEach(p=>p.hidden=p.dataset.panel!=='apps');});
await page.locator('#world-preview').scrollIntoViewIfNeeded();
await page.waitForSelector('#world-preview[data-ready="true"]', {timeout: 8000});
if (software && await page.locator('#world-preview').getAttribute('data-renderer') !== 'canvas') throw Error('Software renderer was not selected');
await page.waitForTimeout(1500);
if (software) {
  const painted = await page.locator('#world-canvas').evaluate(canvas => {
    const pixels = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data;
    const colors = new Set();
    for (let i = 0; i < pixels.length; i += 4 * 19) {
      if (pixels[i + 3]) colors.add(`${pixels[i]},${pixels[i + 1]},${pixels[i + 2]}`);
    }
    return colors.size;
  });
  if (painted < 20) throw Error('Software preview did not paint the scene');
}


console.log('ready',await page.locator('#world-preview').getAttribute('data-continents'));
await page.locator('#world-motion').click();
await page.waitForTimeout(250);
await page.locator('#world-canvas').focus();
const initial=await page.locator('#world-canvas').screenshot();
await page.waitForTimeout(200);
if(!initial.equals(await page.locator('#world-canvas').screenshot()))throw Error('Pause did not stop animation');
await page.locator('#world-canvas').focus();await page.keyboard.press('ArrowRight');await page.waitForTimeout(150);
if(initial.equals(await page.locator('#world-canvas').screenshot()))throw Error('Keyboard rotation did not change image');
await page.locator('#world-motion').click();
await page.waitForTimeout(250);
const moving=await page.locator('#world-canvas').screenshot();
await page.waitForTimeout(300);
if(moving.equals(await page.locator('#world-canvas').screenshot()))throw Error('Resume did not animate the inhabitants');
await page.locator('.app input[value="jitsi"]').check();
if(!(await page.locator('#world-preview').getAttribute('data-continents')).includes('jitsi'))throw Error('Missing Meet island');
await page.locator('#world-legend button[data-app="jitsi"]').click();
await page.waitForTimeout(300);

await page.locator('.app input[value="nextcloud"]').uncheck();
await page.locator('.app input[value="euro-office"]').check();
if(!(await page.locator('#world-preview').getAttribute('data-continents')).includes('nextcloud'))throw Error('Dependency not synchronized');
for(const input of await page.locator('.app input[type="checkbox"]').all())await input.check();
await page.locator('.app input[value="collabora"]').check();
console.log('all islands',await page.locator('#world-legend button').count());
await page.setViewportSize({width:390,height:850});
await page.locator('#world-preview').scrollIntoViewIfNeeded();await page.waitForTimeout(200);

if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Mobile overflow');
for(const input of await page.locator('.app input[type="checkbox"]').all())await input.uncheck();
await page.locator('.app input[name="app-choice-office"][value=""]').check();
if(await page.locator('#world-legend button').count())throw Error('Empty selection not reflected');
await page.emulateMedia({reducedMotion:'reduce'});
await page.waitForFunction(()=>document.getElementById('world-motion').textContent==='Resume');
console.log('errors',errors);if(errors.length)throw Error(errors.join('\n'));
const fallback=await browser.newPage();
await fallback.addInitScript(()=>{HTMLCanvasElement.prototype.getContext=()=>null;});
await fallback.goto(process.argv[2]);
await fallback.waitForSelector('.app input',{state:'attached'});
await fallback.evaluate(()=>{document.querySelectorAll('.panel').forEach(p=>p.hidden=p.dataset.panel!=='apps');});
await fallback.locator('#world-preview').scrollIntoViewIfNeeded();
await fallback.waitForFunction(()=>document.getElementById('world-fallback').textContent.includes('unavailable'));
await fallback.locator('.app input[value="jitsi"]').check();
if(!await fallback.locator('.app input[value="jitsi"]').isChecked())throw Error('Fallback blocked selection');
console.log('Globe checks passed: rendering, pause, rotation, selection, Office dependencies, mobile, empty world, reduced motion and WebGL fallback.');
await browser.close();
