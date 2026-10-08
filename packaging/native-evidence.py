#!/usr/bin/env python3
"""Bundle verified isolated production screenshots without diagnostic logs/secrets."""
import argparse,hashlib,json,struct,subprocess,zipfile
from pathlib import Path
from release import ROOT

def package(source,output,version):
    commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    entries=[];reports=[]
    for platform in ('linux','windows','darwin'):
        for arch in ('amd64','arm64'):
            directory=source/f'native-diagnostics-{platform}-{arch}'
            report_file=next(iter(directory.rglob('report.json')),None)
            if report_file is None:raise ValueError(f'Missing actual native report: {platform}/{arch}')
            report=json.loads(report_file.read_text())
            if any(report.get(k)!=v for k,v in {'platform':platform,'arch':arch,'version':version,'commit':commit,'production':True,'fixtureSynthetic':True,'ownedWindow':True}.items()):raise ValueError(f'Native report metadata mismatch: {platform}/{arch}')
            name=f'{platform}-{arch}.png';image=next(iter(directory.rglob(name)),None)
            if image is None:raise ValueError('Missing native screenshot: '+name)
            data=image.read_bytes()
            if len(data)<4096 or data[:8]!=b'\x89PNG\r\n\x1a\n' or data[12:16]!=b'IHDR':raise ValueError('Invalid native PNG: '+name)
            width,height=struct.unpack('>II',data[16:24])
            if width<800 or height<600:raise ValueError('Native screenshot is too small: '+name)
            report['sha256']=hashlib.sha256(data).hexdigest();reports.append(report)
            entries += [(name,data),(f'{platform}-{arch}.json',(json.dumps(report,indent=2)+'\n').encode())]
    entries.append(('manifest.json',(json.dumps({'version':version,'commit':commit,'reports':reports},indent=2)+'\n').encode()))
    with zipfile.ZipFile(output,'w',compression=zipfile.ZIP_DEFLATED) as package:
        for name,data in sorted(entries):package.writestr(name,data)
    output.with_name(output.name+'.sha256').write_text(hashlib.sha256(output.read_bytes()).hexdigest()+'  '+output.name+'\n')
    print('PASS six actual production screenshots and reports for',version,commit)
if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--source',type=Path,required=True);p.add_argument('--output',type=Path,required=True);p.add_argument('--version',required=True);a=p.parse_args();package(a.source,a.output,a.version)
