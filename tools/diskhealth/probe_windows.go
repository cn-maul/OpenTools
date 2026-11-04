//go:build windows

package diskhealth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

type physicalDisk struct {
	DeviceID               string `json:"deviceID"`
	FriendlyName           string `json:"friendlyName"`
	Serial                 string `json:"serial"`
	Firmware               string `json:"firmware"`
	MediaType              string `json:"mediaType"`
	BusType                string `json:"busType"`
	HealthStatus           string `json:"healthStatus"`
	OperationalStatus      string `json:"operationalStatus"`
	SizeBytes              int64  `json:"sizeBytes"`
	DiskNumber             int    `json:"diskNumber"`
	Temperature            int    `json:"temperature"`
	Wear                   int    `json:"wear"`
	PowerOnHours           int    `json:"powerOnHours"`
	ReadErrorsTotal        int64  `json:"readErrorsTotal"`
	ReadErrorsUncorrected  int64  `json:"readErrorsUncorrected"`
	WriteErrorsTotal       int64  `json:"writeErrorsTotal"`
	WriteErrorsUncorrected int64  `json:"writeErrorsUncorrected"`
	StartStopCycles        int64  `json:"startStopCycles"`
	DriveLetters           string `json:"driveLetters"`
}

type logicalVolume struct {
	DriveLetter string `json:"driveLetter"`
	Label       string `json:"label"`
	Filesystem  string `json:"filesystem"`
	Health      string `json:"health"`
	SizeBytes   int64  `json:"sizeBytes"`
	FreeBytes   int64  `json:"freeBytes"`
}

type snapshot struct {
	CollectedAt string          `json:"collectedAt"`
	IsAdmin     bool            `json:"isAdmin"`
	Disks       []physicalDisk  `json:"disks"`
	Volumes     []logicalVolume `json:"volumes"`
}

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	output, err := runPowerShell(ctx, diskHealthScript)
	if err != nil {
		return snapshot{}, err
	}
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return snapshot{}, fmt.Errorf("磁盘健康检测没有返回数据")
	}
	var value snapshot
	if err := json.Unmarshal(output, &value); err != nil {
		return snapshot{}, fmt.Errorf("解析磁盘健康数据失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return value, nil
}

const diskHealthScript = `
$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
$result = [ordered]@{
    collectedAt = (Get-Date).ToString("o")
    isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    disks = @()
    volumes = @()
}

function IntOrZero($value) {
    if ($null -eq $value) { return 0 }
    try { return [int64]$value } catch { return 0 }
}

foreach ($disk in @(Get-PhysicalDisk)) {
    $counter = $disk | Get-StorageReliabilityCounter
    $letters = @()
    $partitions = @(Get-Disk -Number $disk.DeviceId -ErrorAction SilentlyContinue | Get-Partition -ErrorAction SilentlyContinue)
    foreach ($partition in $partitions) {
        if ($partition.DriveLetter) { $letters += ([string]$partition.DriveLetter) }
    }
    $media = [string]$disk.MediaType
    if ($media -eq "Unspecified" -and ([string]$disk.BusType -eq "NVMe")) { $media = "SSD" }
    $status = @()
    foreach ($item in @($disk.OperationalStatus)) { $status += [string]$item }
    $result.disks += [ordered]@{
        deviceID = [string]$disk.DeviceId
        friendlyName = [string]$disk.FriendlyName
        serial = [string]$disk.SerialNumber
        firmware = [string]$disk.FirmwareVersion
        mediaType = $media
        busType = [string]$disk.BusType
        healthStatus = [string]$disk.HealthStatus
        operationalStatus = ($status -join ",")
        sizeBytes = (IntOrZero $disk.Size)
        diskNumber = (IntOrZero $disk.DeviceId)
        temperature = (IntOrZero $counter.Temperature)
        wear = (IntOrZero $counter.Wear)
        powerOnHours = (IntOrZero $counter.PowerOnHours)
        readErrorsTotal = (IntOrZero $counter.ReadErrorsTotal)
        readErrorsUncorrected = (IntOrZero $counter.ReadErrorsUncorrected)
        writeErrorsTotal = (IntOrZero $counter.WriteErrorsTotal)
        writeErrorsUncorrected = (IntOrZero $counter.WriteErrorsUncorrected)
        startStopCycles = (IntOrZero $counter.StartStopCycleCount)
        driveLetters = ($letters -join ", ")
    }
}

foreach ($volume in @(Get-Volume)) {
    if (-not $volume.DriveLetter) { continue }
    $result.volumes += [ordered]@{
        driveLetter = [string]$volume.DriveLetter
        label = [string]$volume.FileSystemLabel
        filesystem = [string]$volume.FileSystem
        health = [string]$volume.HealthStatus
        sizeBytes = (IntOrZero $volume.Size)
        freeBytes = (IntOrZero $volume.SizeRemaining)
    }
}

$result | ConvertTo-Json -Depth 6 -Compress
`
