Add-Type -AssemblyName System.Drawing

$sourcePath = (Resolve-Path 'assets\logo-ico.png').Path
$targetDir = Join-Path (Get-Location).Path 'cmd\setupctl\winres'

$sizes = @(
	@{ Name = 'icon16.png'; Size = 16 },
	@{ Name = 'icon32.png'; Size = 32 },
	@{ Name = 'icon48.png'; Size = 48 },
	@{ Name = 'icon256.png'; Size = 256 }
)

$img = [System.Drawing.Image]::FromFile($sourcePath)
try {
	foreach ($entry in $sizes) {
		$bmp = New-Object System.Drawing.Bitmap($entry.Size, $entry.Size)
		$gfx = [System.Drawing.Graphics]::FromImage($bmp)
		try {
			$gfx.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
			$gfx.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
			$gfx.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
			$gfx.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
			$gfx.DrawImage($img, 0, 0, $entry.Size, $entry.Size)
		}
		finally {
			$gfx.Dispose()
		}

		try {
			$bmp.Save((Join-Path $targetDir $entry.Name), [System.Drawing.Imaging.ImageFormat]::Png)
		}
		finally {
			$bmp.Dispose()
		}
	}
}
finally {
	$img.Dispose()
}
