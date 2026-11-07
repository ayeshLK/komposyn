#!/usr/bin/env python3
# Copyright 2026 Ayesh Almeida
# SPDX-License-Identifier: Apache-2.0
"""Bounded isolated native acceptance of the #243 service plus a test-only overlay."""
import argparse
import hashlib
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time

BASELINE = 'd43a7dd7005ae6ccc8102936d00bd2920bbd9fcf'
EXPECTED = ['TestNativeShippedGenericReadGate', 'TestNativeServicePositiveRead',
            'TestNativeServiceProtectedRoutesAndAliases', 'TestNativeServiceRotationJoinsHeldWorker',
            'TestNativeServicePostCaptureRevocation', 'TestNativeServiceAuthRotationJoinsHeldWorker',
            'TestNativeServiceRootChangeWhileHeld','TestNativeServiceCancellationWhileHeld',
            'TestNativeServiceShutdownRetryWhileHeld']

def digest(path):
    with open(path, 'rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()

def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, default=Path.cwd())
    parser.add_argument('--revision', default=BASELINE)
    parser.add_argument('--bwrap', type=Path, default=Path('/usr/bin/bwrap'))
    args = parser.parse_args()
    if sys.platform != 'linux':
        parser.error('Run on a normal Linux host; do not use sudo.')
    if os.geteuid()==0:
        parser.error('Run as your normal user, not root/sudo')
    if not args.bwrap.is_absolute():
        parser.error('--bwrap must be an absolute path')
    if not shutil.which('go') or not shutil.which('git'):
        parser.error('Go and Git must be installed')
    os.umask(0o077)
    bundle = Path(__file__).resolve().parent
    overlay = bundle / 'native_acceptance_linux_test.go'
    if not overlay.is_file():
        parser.error('Keep the Go fixture file beside this script')
    # Rev-parse prevents revision text being interpreted as an archive option.
    if not re.fullmatch(r'[0-9a-fA-F]{40}',args.revision):
        parser.error('--revision must be a full commit hash')
    revision=args.revision.lower()
    out = Path(tempfile.mkdtemp(prefix='komposyn-composition-native-results.', dir='/tmp'))
    work = Path(tempfile.mkdtemp(prefix='komposyn-composition-native-work.', dir='/tmp'))
    records = out/'native-records'; records.mkdir()
    source = work/'source'; source.mkdir()
    fixture_tmp = work/'fixtures'; fixture_tmp.mkdir()
    manifest = {'schema_version':1, 'harness_revision':2, 'source_commit':revision, 'test_overlay':True,
        'runner_sha256':digest(Path(__file__)), 'overlay_sha256':digest(overlay),
        'python_version':sys.version.split()[0],
        'run_time_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
        'scope':'PR #243 service fixture and coordinator publication; no model-facing reads/provider network',
        'stages':{}, 'status':'incomplete', 'work_retained':True}
    clean = True
    exit_code = 1
    try:
        archive_repo=args.repo
        exists=subprocess.run(['git','-C',str(archive_repo),'cat-file','-e',revision+'^{commit}'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15).returncode==0
        if not exists:
            # Fetch only into scratch space; never mutate the user's checkout.
            archive_repo=work/'revision-repo'
            subprocess.run(['git','init','-q',str(archive_repo)],check=True,timeout=15)
            subprocess.run(['git','-C',str(archive_repo),'fetch','--quiet','--depth=1','https://github.com/ayeshLK/komposyn.git',revision],check=True,timeout=60,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
            manifest['source_fetched_into_scratch']=True
        data = subprocess.check_output(['git','-C',str(archive_repo),'archive','--format=tar',revision],timeout=30)
        manifest['source_archive_sha256'] = hashlib.sha256(data).hexdigest()
        with tarfile.open(fileobj=io.BytesIO(data)) as archive:
            archive.extractall(source, filter='data')
        required = source/'internal/harness/sandbox/service_linux.go'
        if not required.is_file():
            raise RuntimeError('Selected revision lacks the merged host service')
        destination = source/'internal/harness/sandbox/native_acceptance_overlay_linux_test.go'
        if destination.exists():
            raise RuntimeError('Overlay target already exists; refusing overwrite')
        shutil.copyfile(overlay,destination)
        shutil.copyfile(overlay,out/'native_acceptance_overlay.go.txt')
        helper = source/'scripts/read-broker-study-capture.py'
        manifest['capture_helper_sha256']=digest(helper)
        # Preserve the exact bounded capture helper used for this run.
        shutil.copyfile(helper,out/'capture-helper.py.txt')
        spec=importlib.util.spec_from_file_location('bounded_capture',helper)
        capture=importlib.util.module_from_spec(spec);spec.loader.exec_module(capture)
        os.environ['TMPDIR']=str(fixture_tmp)
        os.environ['KOMPOSYN_NATIVE_RECORDS']=str(records)
        os.environ['KOMPOSYN_NATIVE_BWRAP']=str(args.bwrap)
        os.environ.setdefault('GOCACHE',str(work/'go-cache'))
        os.environ['CGO_ENABLED']='0'
        os.environ['GOWORK']='off'
        manifest['go_version']=subprocess.check_output(['go','version'],text=True,timeout=30).strip()
        manifest['kernel']=os.uname().release
        manifest['uid']=os.getuid()
        def stage(name,argv,timeout):
            value=capture.run_capture(argv,timeout,2*1024*1024,str(out/(name+'.stdout.log')),str(out/(name+'.stderr.log')),str(source))
            manifest['stages'][name]=value
            write_json(out/(name+'.capture.json'),value)
            nonlocal clean
            ok_cleanup=value.get('cleanup_complete') is True and value.get('direct_child_reaped') is True and not value.get('descendants_remaining')
            clean=clean and ok_cleanup
            ok=ok_cleanup and value.get('exit_code')==0 and not value.get('timed_out') and not value.get('stdout_truncated') and not value.get('stderr_truncated') and not value.get('output_drain_incomplete')
            print(name+': '+('completed' if ok else 'failed/incomplete'),flush=True)
            return ok
        # Unit/coordinator checks remain a separately labelled evidence stage.
        unit_ok=stage('unit-checks',['go','test','-json','-count=1','-timeout=90s','-run','^Test(HostComposition|FailedOwner|PreparationRefusal|CancellationAfterPreparation|UnconfirmedPreparation|ChatCloseContext)','./internal/harness/sandbox','./internal/harness/runtime'],180)
        binary=work/'native-service-tests'
        build_ok=stage('build-native',['go','test','-c','-buildvcs=false','-o',str(binary),'./internal/harness/sandbox'],300)
        if build_ok:
            manifest['native_binary_sha256']=digest(binary)
            run_ok=stage('native',[str(binary),'-test.v','-test.count=1','-test.timeout=240s','-test.run=^TestNative'],270)
            rows=[]
            for case in EXPECTED:
                path=records/(case+'.json')
                rows.append(json.loads(path.read_text()) if path.exists() else {'case':case,'status':'missing','cleanup_confirmed':False})
            write_json(out/'native-report.json',{'source_commit':revision,'overlay_sha256':digest(overlay),'cases':rows,
                'limitations':['Test-only fixture authorizer rebinding; generic reads remain blocked in shipped service.',
                    'No external provider request; private coordinator publication only.',
                    'Native fixed-worker cases do not establish all enablement gates, sealed-copy modes, arbitrary descendants, unknown data policy, or all platform guarantees.',
                    'Existing unit tests are not counted as native worker enforcement evidence.']})
            if unit_ok and run_ok and all(r['status']=='measured_pass' and r['cleanup_confirmed'] for r in rows):
                manifest['status']='scoped_native_pass';exit_code=0
            elif run_ok and any(r['status']=='unavailable' for r in rows) and all(r['status'] in ['measured_pass','unavailable'] for r in rows) and unit_ok:
                manifest['status']='native_unavailable';exit_code=2
            else:
                manifest['status']='failed_or_incomplete'
        else:
            manifest['status']='build_failed_or_incomplete'
    except Exception as exc:
        manifest['runner_error_type']=type(exc).__name__
        print('Runner incomplete: '+type(exc).__name__,file=sys.stderr)
    finally:
        # Logs contain synthetic fixture diagnostics only. Remove host/source paths.
        replacements=[(str(work),'<work>'),(str(args.repo.resolve()),'<repo>'),(str(Path.home()),'<home>'),(str(out),'<results>')]
        for log in out.glob('*.log'):
            text=log.read_text(errors='replace')
            for a,b in replacements:
                if a and a!='/':text=text.replace(a,b)
            log.write_text(text)
        if clean:
            shutil.rmtree(work)
            manifest['work_retained']=False
        else:
            # Do not remove resources while process cleanup remains unconfirmed.
            print('Cleanup incomplete; synthetic work retained at '+str(work),file=sys.stderr)
            manifest['status']='cleanup_incomplete';exit_code=1
        write_json(out/'manifest.json',manifest)
        summary=['Status: '+manifest['status'],'Source: '+revision,
            'Test overlay SHA-256: '+manifest['overlay_sha256'],
            'This is scoped service fixture evidence, not tool enablement.',
            'Review native-report.json, native-records and capture cleanup status.']
        (out/'summary.txt').write_text('\n'.join(summary)+'\n')
        (out/'SHA256SUMS').write_text(''.join(digest(p)+'  '+str(p.relative_to(out))+'\n' for p in sorted(out.rglob('*')) if p.is_file()))
        archive=Path(str(out)+'.tar.gz')
        with tarfile.open(archive,'w:gz') as tf:tf.add(out,arcname=out.name)
        Path(str(archive)+'.sha256').write_text(digest(archive)+'  '+archive.name+'\n')
        print('Results archive: '+str(archive),flush=True)
    return exit_code

if __name__=='__main__':
    sys.exit(main())
