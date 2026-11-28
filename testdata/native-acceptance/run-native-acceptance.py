#!/usr/bin/env python3
# Copyright 2026 Ayesh Almeida
# SPDX-License-Identifier: Apache-2.0
"""Bounded isolated native acceptance of sandbox services and #321 creation activation."""
import argparse
import hashlib
import importlib.util
import io
import json
import os
import platform
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time

DEFAULT_EXPECTED = ['TestNativeActorModeDispatch', 'TestNativeShippedGenericReadGate', 'TestNativeServicePositiveRead',
            'TestNativeServiceProtectedRoutesAndAliases', 'TestNativeServiceRotationJoinsHeldWorker',
            'TestNativeServiceMetadataFIEMAP',
            'TestNativeServicePostCaptureRevocation', 'TestNativeServiceAuthRotationJoinsHeldWorker',
            'TestNativeServiceBoundedCreation', 'TestNativeServiceCreationUsesRetainedParentAfterIndependentMove',
            'TestNativeServiceNestedCreationUsesStartupScope', 'TestNativeServiceStartupRootMovementIsTerminal',
            'TestNativeServiceNestedParentSubstitutionRefusesPreparedProposal',
            'TestNativeServiceScopeAndTargetRefusalsPreserveFixtures', 'TestNativeServiceMountBoundaryRefusal',
            'TestNativeServiceStartupRootMovementAfterPublicationRelease',
            'TestNativeServiceRootChangeWhileHeld','TestNativeServiceCancellationWhileHeld',
            'TestNativeServiceShutdownRetryWhileHeld', 'TestNativeRewritePrototypeComposedReadAndApprovedRewrite']
EXPECTED = list(DEFAULT_EXPECTED)

def digest(path):
    with open(path, 'rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()

def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')

def mount_filesystem(path):
    target = str(path.resolve())
    best = None
    try:
        for line in Path('/proc/self/mountinfo').read_text().splitlines():
            fields = line.split()
            separator = fields.index('-')
            mountpoint = fields[4].replace('\\040', ' ').replace('\\011', '\t').replace('\\134', '\\')
            if target == mountpoint or target.startswith(mountpoint.rstrip('/') + '/'):
                if best is None or len(mountpoint) > len(best[0]):
                    best = (mountpoint, fields[separator + 1])
    except (OSError, ValueError, IndexError):
        return 'unavailable'
    return best[1] if best else 'unavailable'

def lsm_profile():
    try:
        value = Path('/sys/kernel/security/lsm').read_text().strip()
        return value if value else 'unavailable: /sys/kernel/security/lsm is empty'
    except OSError as exc:
        return 'unavailable: /sys/kernel/security/lsm could not be read (' + type(exc).__name__ + ')'

def main():
    global EXPECTED
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, default=Path.cwd())
    parser.add_argument('--revision', required=True, help='full source commit containing the candidate under test')
    parser.add_argument('--bwrap', type=Path, default=Path('/usr/bin/bwrap'))
    parser.add_argument('--scratch-dir', type=Path, default=Path('/tmp'),
        help='existing writable directory on the filesystem used for disposable fixtures and result bundle')
    parser.add_argument('--mount-scope', type=Path, default=None,
        help='optional existing private directory beneath scratch-dir with an already-mounted child; the test never creates or changes mounts')
    parser.add_argument('--case', choices=DEFAULT_EXPECTED,
        help='run and report only this expected native case; without it, validate the full default inventory')
    args = parser.parse_args()
    if args.case:
        EXPECTED = [args.case]
    if sys.platform != 'linux':
        parser.error('Run on a normal Linux host; do not use sudo.')
    if os.geteuid()==0:
        parser.error('Run as your normal user, not root/sudo')
    if not args.bwrap.is_absolute():
        parser.error('--bwrap must be an absolute path')
    scratch_dir=args.scratch_dir.resolve()
    if not scratch_dir.is_dir() or not os.access(scratch_dir, os.W_OK | os.X_OK):
        parser.error('--scratch-dir must be an existing writable directory')
    mount_scope = None
    if args.mount_scope is not None:
        if not args.mount_scope.is_absolute():
            parser.error('--mount-scope must be an absolute path')
        try:
            mount_scope = args.mount_scope.resolve(strict=True)
            mount_scope.relative_to(scratch_dir)
            mount_stat = os.lstat(args.mount_scope)
        except (OSError, ValueError):
            parser.error('--mount-scope must be an existing directory beneath --scratch-dir')
        if (mount_scope == scratch_dir or not os.path.isdir(mount_scope) or os.path.islink(args.mount_scope) or
                mount_stat.st_uid != os.getuid() or mount_stat.st_mode & 0o077):
            parser.error('--mount-scope must be a private real directory owned by the current user')
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
    out = Path(tempfile.mkdtemp(prefix='komposyn-composition-native-results.', dir=scratch_dir))
    work = Path(tempfile.mkdtemp(prefix='komposyn-composition-native-work.', dir=scratch_dir))
    records = out/'native-records'; records.mkdir()
    source = work/'source'; source.mkdir()
    fixture_tmp = work/'fixtures'; fixture_tmp.mkdir()
    scratch_filesystem = mount_filesystem(scratch_dir)
    manifest = {'schema_version':1, 'harness_revision':4, 'source_commit':revision, 'test_overlay':True,
        'runner_sha256':digest(Path(__file__)), 'overlay_sha256':digest(overlay),
        'expected_cases':EXPECTED,
        'python_version':sys.version.split()[0],
        'run_time_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
        'scope':'PR #243 service fixtures bounded #321 creation worker, #322 metadata-only probe, and #322 test-only native rewrite acceptance; no production rewrite tool registration/provider network',
        'selected_case':args.case,
        'scratch_directory_configured':True,
        'scratch_filesystem':scratch_filesystem,
        'scratch_mountpoint':'<scratch-mount>' if scratch_filesystem != 'unavailable' else 'unavailable',
        'lsm_profile':lsm_profile(),
        'bubblewrap_path':'<configured-backend>',
        'bubblewrap_metadata':{},
        'mount_scope_supplied':mount_scope is not None,
        'stages':{}, 'status':'incomplete', 'work_retained':True}
    clean = True
    exit_code = 1
    try:
        try:
            backend_stat = args.bwrap.stat()
            manifest['bubblewrap_metadata'] = {'uid':backend_stat.st_uid, 'mode':oct(backend_stat.st_mode & 0o7777),
                'version':'unavailable until the trusted-executable check passes',
                'bind_mode':'unavailable until a trusted native worker is launched'}
        except OSError:
            manifest['bubblewrap_metadata'] = {'version':'unavailable: configured Bubblewrap path cannot be stated',
                'bind_mode':'unavailable: no trusted native worker was launched'}
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
        os.environ.pop('KOMPOSYN_NATIVE_MOUNT_SCOPE', None)
        if mount_scope is not None:
            os.environ['KOMPOSYN_NATIVE_MOUNT_SCOPE']=str(mount_scope)
        os.environ.setdefault('GOCACHE',str(work/'go-cache'))
        os.environ['CGO_ENABLED']='0'
        os.environ['GOWORK']='off'
        manifest['go_version']=subprocess.check_output(['go','version'],text=True,timeout=30).strip()
        manifest['kernel']=os.uname().release
        manifest['architecture']=platform.machine()
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
        unit_ok=stage('unit-checks',['go','test','-tags=komposyn_stage6_study','-json','-count=1','-timeout=90s','-run','^Test(MetadataFIEMAPProbe|HostComposition|FailedOwner|PreparationRefusal|CancellationAfterPreparation|UnconfirmedPreparation|ChatCloseContext|StartupScopeMovement|CreationResolvesNested|StartupRootMoveAtPublication|ClassifyFromRetainedDirectory)','./internal/harness/sandbox','./internal/harness/readbrokerstudy','./internal/harness/runtime'],180)
        binary=work/'native-service-tests'
        build_ok=stage('build-native',['go','test','-tags=komposyn_stage6_study','-c','-buildvcs=false','-o',str(binary),'./internal/harness/sandbox'],300)
        if build_ok:
            manifest['native_binary_sha256']=digest(binary)
            test_pattern='^'+re.escape(args.case)+'$' if args.case else '^TestNative'
            run_ok=stage('native',[str(binary),'-test.v','-test.count=1','-test.timeout=240s','-test.run='+test_pattern],270)
            rows=[]
            for case in EXPECTED:
                path=records/(case+'.json')
                try:
                    row=json.loads(path.read_text()) if path.exists() else {'case':case,'status':'missing','cleanup_confirmed':False}
                except (OSError, json.JSONDecodeError):
                    row={'case':case,'status':'invalid_record','cleanup_confirmed':False}
                if row.get('case') != case:
                    row['status']='case_identity_mismatch'
                if case == 'TestNativeServiceNestedCreationUsesStartupScope' and row.get('creation_schema_registered') is not True and row.get('status') == 'measured_pass':
                    row['status']='shipping_creation_schema_not_registered'
                if row.get('status') == 'unavailable' and not row.get('unavailable_reason'):
                    row['status']='unavailability_unexplained'
                rows.append(row)
            record_names={p.name for p in records.glob('*.json')}
            expected_names={case+'.json' for case in EXPECTED}
            inventory_exact=(len(set(EXPECTED)) == len(EXPECTED) and
                record_names == expected_names and len(rows) == len(EXPECTED))
            write_json(out/'native-report.json',{'source_commit':revision,'overlay_sha256':digest(overlay),'cases':rows,
                'expected_cases':EXPECTED,'case_inventory_exact':inventory_exact,
                'limitations':['Test-only fixture authorizer rebinding; generic reads remain blocked in shipped service.',
                    'No external provider request; private coordinator publication only.',
                    'Native fixed-worker cases include profile-gated apply_patch schema registration and one opt-in test-only rewrite path, but do not establish production rewrite registration, live provider/UI behavior, sealed-copy modes, arbitrary descendants, unknown data policy, or all platform guarantees.',
                    'Existing unit tests are not counted as native worker enforcement evidence.']})
            records_valid=inventory_exact and all(r.get('cleanup_confirmed') is True and r.get('status') in ('measured_pass','unavailable') for r in rows)
            if unit_ok and run_ok and records_valid and all(r['status']=='measured_pass' for r in rows):
                manifest['status']='scoped_native_pass';exit_code=0
            elif run_ok and records_valid and any(r['status']=='unavailable' for r in rows) and unit_ok:
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
        manifest['exit_code']=exit_code
        write_json(out/'manifest.json',manifest)
        summary=['Status: '+manifest['status'],'Source: '+revision,
            'Exit code: '+str(exit_code),
            'Test overlay SHA-256: '+manifest['overlay_sha256'],
            'This measures the profile-gated runtime registration path and native Service effects on one host profile; maintainer review and release remain separate.',
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
