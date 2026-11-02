<#
.SYNOPSIS
    Temukan konfigurasi AD dari mesin Windows yang join domain (ADSI).
.DESCRIPTION
    Membaca RootDSE (domain, base DN, FQDN DC) dan mencari DN grup dari
    namanya, lalu mencetak satu baris JSON ke stdout yang bisa ditempel
    ke panel.json atau dibaca deploy-panel-production.ps1 (-AdDiscover).
    Dijalankan sebagai user domain biasa; tidak mengubah apa pun kecuali
    bila -CaFile atau -PanelJson diisi. Pesan progres lewat Write-Host
    (tidak masuk stdout), jadi output JSON aman di-capture script lain.
    Gagal apa pun = pesan jelas + exit code 1, bukan hasil setengah-setengah.
    Prasyarat: mesin ini join domain dan user yang menjalankan bisa
    membaca AD (user domain biasa cukup; tidak butuh admin kecuali untuk
    menulis ke folder yang dilindungi seperti C:\Panel).
    Catatan: nama grup tidak bisa ditebak - harus diisi lewat -GroupName.
.PARAMETER GroupName
    Nama grup IT (name), default "IS". Bila diisi DN lengkap (mengandung
    "="), DN itu dipakai langsung (dicek keberadaannya).
.PARAMETER CaFile
    Bila diisi, export sertifikat CA penerbit sertifikat LDAPS DC ke
    path ini dalam format PEM (yang dibaca panel; bukan DER). Bila CA
    tidak ditemukan, script gagal dengan pesan jelas.
.PARAMETER PanelJson
    Bila diisi, tulis nilai yang ditemukan ke panel.json di path ini
    (backup panel.json.bak-discover-<timestamp> dulu; tulis tanpa BOM
    karena Go menolak BOM). Tanpa ini, script hanya mencetak JSON.
    Menulis ke panel.json juga menghapus ad_dummy_users bila ada (mode
    AD sungguhan menggantikan dummy) dan memaksa ad_allow_insecure_ldap
    menjadi false.
.EXAMPLE
    .\discover-ad.ps1 -GroupName IS
.EXAMPLE
    .\discover-ad.ps1 -GroupName IS -CaFile C:\Panel\dc-ca.crt -PanelJson C:\Panel\panel.json
#>
[CmdletBinding()]
param(
    [string]$GroupName = "IS",
    [string]$CaFile = "",
    [string]$PanelJson = ""
)

$ErrorActionPreference = "Stop"

function ConvertFrom-DistinguishedNameToDomain {
    param([string]$Dn)
    $parts = $Dn -split '(?<!\\),'
    $dcParts = @()
    foreach ($p in $parts) {
        $t = $p.Trim()
        if ($t -match '^(?i)DC=(.*)$') { $dcParts += $Matches[1] }
    }
    if ($dcParts.Count -eq 0) { throw "Base DN '$Dn' tidak mengandung komponen DC=." }
    return ($dcParts -join '.').ToLowerInvariant()
}

function Build-AdServerUrl {
    param([string]$DcFqdn)
    $hostName = $DcFqdn.Trim()
    if ([string]::IsNullOrWhiteSpace($hostName)) { throw "FQDN DC kosong." }
    if ($hostName -match '^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$') { throw "DC harus FQDN, bukan IP ($hostName); LDAPS butuh hostname untuk validasi sertifikat." }
    if ($hostName -notmatch '\.') { throw "FQDN DC harus mengandung titik ($hostName); ini terlihat seperti NetBIOS, bukan FQDN." }
    return "ldaps://$($hostName):636"
}

function Escape-LdapFilterValue {
    param([string]$Value)
    $v = $Value -replace '\\', '\5c'
    $v = $v -replace '\*', '\2a'
    $v = $v -replace '\(', '\28'
    $v = $v -replace '\)', '\29'
    return $v
}

function Select-GroupDistinguishedName {
    param([string[]]$Dns, [string]$Filter)
    if ($Dns.Count -eq 0) { throw "Grup tidak ditemukan (filter $Filter). Cek nama grup di ADUC, atau isi DN lengkap lewat -GroupName." }
    if ($Dns.Count -gt 1) { throw "Nama grup ambigu, ditemukan $($Dns.Count) grup:`n$($Dns -join "`n")`nPakai DN lengkap lewat -GroupName." }
    return $Dns[0]
}

function Export-DcCaCertificate {
    param([string]$DcHost, [string]$CaFile)
    $tcp = New-Object Net.Sockets.TcpClient($DcHost, 636)
    try {
        $ssl = New-Object Net.Security.SslStream($tcp.GetStream(), $false, { $true })
        $ssl.AuthenticateAsClient($DcHost)
        $remote = $ssl.RemoteCertificate
        if ($null -eq $remote) { throw "DC tidak menyajikan sertifikat LDAPS." }
        $cert2 = New-Object Security.Cryptography.X509Certificates.X509Certificate2($remote)
        $chain = New-Object Security.Cryptography.X509Certificates.X509Chain
        [void]$chain.Build($cert2)
        $anchor = $null
        if ($chain.ChainElements.Count -gt 0) {
            $anchor = $chain.ChainElements[$chain.ChainElements.Count - 1].Certificate
        } else {
            $store = New-Object Security.Cryptography.X509Certificates.X509Store("Root", "LocalMachine")
            $store.Open("ReadOnly")
            try {
                $match = @($store.Certificates | Where-Object { $_.Subject -eq $remote.Issuer })
                if ($match.Count -gt 0) { $anchor = $match[0] }
            } finally {
                $store.Close()
            }
        }
        if ($null -eq $anchor) { throw "CA penerbit sertifikat DC (issuer: $($remote.Issuer)) tidak ditemukan di store. Isi -AdCaFile manual, atau kosongkan (panel memakai system roots)." }
        if ($anchor.Thumbprint -eq $cert2.Thumbprint) {
            Write-Host "    PERHATIAN: sertifikat DC self-signed; file CA berisi sertifikat DC itu sendiri (pinning)." -ForegroundColor Yellow
        }
        $dir = Split-Path $CaFile -Parent
        if (-not [string]::IsNullOrWhiteSpace($dir) -and -not (Test-Path $dir)) {
            New-Item -ItemType Directory -Path $dir -Force | Out-Null
        }
        $b64 = [Convert]::ToBase64String($anchor.GetRawCertData(), 'InsertLineBreaks')
        $pem = "-----BEGIN CERTIFICATE-----`n" + $b64 + "`n-----END CERTIFICATE-----`n"
        [System.IO.File]::WriteAllText($CaFile, $pem, (New-Object Text.UTF8Encoding($false)))
        return $CaFile
    } finally {
        $tcp.Close()
    }
}

function Write-PanelJsonAdValues {
    param([string]$Path, $Values)
    $obj = $null
    if (Test-Path $Path) {
        try {
            $obj = [System.IO.File]::ReadAllText($Path) | ConvertFrom-Json
        } catch {
            throw "panel.json di $Path rusak (bukan JSON valid): $($_.Exception.Message)"
        }
    }
    if ($null -eq $obj) { $obj = [pscustomobject]@{} }
    $obj.ad_server = $Values.ad_server
    $obj.ad_domain = $Values.ad_domain
    $obj.ad_base_dn = $Values.ad_base_dn
    $obj.ad_group_dn = $Values.ad_group_dn
    if (-not [string]::IsNullOrWhiteSpace($Values.ad_ca_file)) { $obj.ad_ca_file = $Values.ad_ca_file }
    $obj.ad_enabled = $true
    $obj.ad_allow_insecure_ldap = $false
    if ($null -ne $obj.ad_dummy_users) { $obj.PSObject.Properties.Remove('ad_dummy_users') }
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    if (Test-Path $Path) { Copy-Item $Path "$Path.bak-discover-$stamp" -Force }
    $json = $obj | ConvertTo-Json -Depth 10
    [System.IO.File]::WriteAllText($Path, $json, (New-Object Text.UTF8Encoding($false)))
}

if ($MyInvocation.InvocationName -ne '.') {
    try {
        Write-Host "==> AD discovery: membaca RootDSE ..." -ForegroundColor Cyan
        try {
            $root = [adsi]"LDAP://RootDSE"
            $baseDn = $root.Properties["defaultNamingContext"].Value
            $dcFqdn = $root.Properties["dnsHostName"].Value
        } catch {
            throw "Tidak bisa membaca AD dari mesin ini (bukan join domain, atau ADSI tidak tersedia): $($_.Exception.Message)"
        }
        if ([string]::IsNullOrWhiteSpace($baseDn)) { throw "RootDSE tidak mengembalikan defaultNamingContext." }
        $domain = $env:USERDNSDOMAIN
        if ([string]::IsNullOrWhiteSpace($domain)) { $domain = ConvertFrom-DistinguishedNameToDomain $baseDn }
        if ([string]::IsNullOrWhiteSpace($dcFqdn)) {
            $logon = ($env:LOGONSERVER -replace '^\\\\', '')
            if ([string]::IsNullOrWhiteSpace($logon)) { throw "FQDN DC tidak ditemukan (dnsHostName kosong dan LOGONSERVER kosong)." }
            $dcFqdn = "$logon.$domain"
        }
        $adServer = Build-AdServerUrl $dcFqdn
        Write-Host "    domain   : $domain"
        Write-Host "    base DN  : $baseDn"
        Write-Host "    DC       : $dcFqdn"
        if ($GroupName -match '=') {
            Write-Host "==> Memakai DN grup langsung: $GroupName" -ForegroundColor Cyan
            try {
                $check = [adsi]"LDAP://$GroupName"
                $foundDn = $check.Properties["distinguishedName"].Value
            } catch {
                $foundDn = $null
            }
            if ([string]::IsNullOrWhiteSpace($foundDn)) { throw "DN grup '$GroupName' tidak ditemukan di AD." }
            $groupDn = $foundDn
        } else {
            Write-Host "==> Mencari grup '$GroupName' ..." -ForegroundColor Cyan
            $searcher = New-Object DirectoryServices.DirectorySearcher
            $searcher.SearchRoot = [adsi]"LDAP://$baseDn"
            $searcher.Filter = "(&(objectClass=group)(name=$(Escape-LdapFilterValue $GroupName)))"
            [void]$searcher.PropertiesToLoad.Add("distinguishedName")
            $results = $searcher.FindAll()
            try {
                $dns = @($results | ForEach-Object { $_.Properties["distinguishedname"][0] })
            } finally {
                $results.Dispose()
            }
            $groupDn = Select-GroupDistinguishedName $dns $searcher.Filter
        }
        Write-Host "    grup     : $groupDn"
        $caOut = ""
        if (-not [string]::IsNullOrWhiteSpace($CaFile)) {
            Write-Host "==> Mengambil sertifikat LDAPS DC dan mencari CA penerbit ..." -ForegroundColor Cyan
            $caOut = Export-DcCaCertificate -DcHost $dcFqdn -CaFile $CaFile
            Write-Host "    CA file  : $caOut"
        }
        $result = [ordered]@{
            ad_server  = $adServer
            ad_domain  = $domain
            ad_base_dn = $baseDn
            ad_group_dn = $groupDn
            ad_ca_file = $caOut
        }
        if (-not [string]::IsNullOrWhiteSpace($PanelJson)) {
            Write-PanelJsonAdValues -Path $PanelJson -Values $result
            Write-Host "==> panel.json diperbarui: $PanelJson (backup .bak-discover-*)" -ForegroundColor Cyan
        }
        $result | ConvertTo-Json -Compress
    } catch {
        Write-Host ""
        Write-Host "DISCOVER FAIL: $($_.Exception.Message)" -ForegroundColor Red
        exit 1
    }
}
