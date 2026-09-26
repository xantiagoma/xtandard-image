// IPTC-IIM dataset table copied from imgproxy's imagemeta/iptc/tags.go,
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: identifiers renamed; the table is unexported.

package imagemeta

import "math"

// iptcTags maps record:dataset to its name, title, value format, whether it
// is mandatory and repeatable, and its minimum and maximum size in bytes.
var iptcTags = map[iptcKey]iptcTag{
	{1, 0}: {
		"ModelVersion",
		"Model Version",
		iptcShort, true, false, 2, 2,
	},
	{1, 5}: {
		"Destination",
		"Destination",
		iptcString, false, true, 0, 1024,
	},
	{1, 20}: {
		"FileFormat",
		"File Format",
		iptcShort, true, false, 2, 2,
	},
	{1, 22}: {
		"FileVersion",
		"File Version",
		iptcShort, true, false, 2, 2,
	},
	{1, 30}: {
		"ServiceID",
		"Service Identifier",
		iptcString, true, false, 0, 10,
	},
	{1, 40}: {
		"EnvelopeNum",
		"Envelope Number",
		iptcString, true, false, 8, 8,
	},
	{1, 50}: {
		"ProductID",
		"Product I.D.",
		iptcString, false, true, 0, 32,
	},
	{1, 60}: {
		"EnvelopePriority",
		"Envelope Priority",
		iptcString, false, false, 1, 1,
	},
	{1, 70}: {
		"DateSent",
		"Date Sent",
		iptcDate, true, false, 8, 8,
	},
	{1, 80}: {
		"TimeSent",
		"Time Sent",
		iptcTime, false, false, 11, 11,
	},
	{1, 90}: {
		"CharacterSet",
		"Coded Character Set",
		iptcBinary, false, false, 0, 32,
	},
	{1, 100}: {
		"UNO",
		"Unique Name of Object",
		iptcString, false, false, 14, 80,
	},
	{1, 120}: {
		"ARMID",
		"ARM Identifier",
		iptcShort, false, false, 2, 2,
	},
	{1, 122}: {
		"ARMVersion",
		"ARM Version",
		iptcShort, false, false, 2, 2,
	},
	{2, 0}: {
		"RecordVersion",
		"Record Version",
		iptcShort, true, false, 2, 2,
	},
	{2, 3}: {
		"ObjectType",
		"Object Type Reference",
		iptcString, false, false, 3, 67,
	},
	{2, 4}: {
		"ObjectAttribute",
		"Object Attribute Reference",
		iptcString, false, true, 4, 68,
	},
	{2, 5}: {
		"ObjectName",
		"Object Name",
		iptcString, false, false, 0, 64,
	},
	{2, 7}: {
		"EditStatus",
		"Edit Status",
		iptcString, false, false, 0, 64,
	},
	{2, 8}: {
		"EditorialUpdate",
		"Editorial Update",
		iptcString, false, false, 2, 2,
	},
	{2, 10}: {
		"Urgency",
		"Urgency",
		iptcString, false, false, 1, 1,
	},
	{2, 12}: {
		"SubjectRef",
		"Subject Reference",
		iptcString, false, true, 13, 236,
	},
	{2, 15}: {
		"Category",
		"Category",
		iptcString, false, false, 0, 3,
	},
	{2, 20}: {
		"SupplCategory",
		"Supplemental Category",
		iptcString, false, true, 0, 32,
	},
	{2, 22}: {
		"FixtureID",
		"Fixture Identifier",
		iptcString, false, false, 0, 32,
	},
	{2, 25}: {
		"Keywords",
		"Keywords",
		iptcString, false, true, 0, 64,
	},
	{2, 26}: {
		"ContentLocCode",
		"Content Location Code",
		iptcString, false, true, 3, 3,
	},
	{2, 27}: {
		"ContentLocName",
		"Content Location Name",
		iptcString, false, true, 0, 64,
	},
	{2, 30}: {
		"ReleaseDate",
		"Release Date",
		iptcDate, false, false, 8, 8,
	},
	{2, 35}: {
		"ReleaseTime",
		"Release Time",
		iptcTime, false, false, 11, 11,
	},
	{2, 37}: {
		"ExpirationDate",
		"Expiration Date",
		iptcDate, false, false, 8, 8,
	},
	{2, 38}: {
		"ExpirationTime",
		"Expiration Time",
		iptcTime, false, false, 11, 11,
	},
	{2, 40}: {
		"SpecialInstructions",
		"Special Instructions",
		iptcString, false, false, 0, 256,
	},
	{2, 42}: {
		"ActionAdvised",
		"Action Advised",
		iptcString, false, false, 2, 2,
	},
	{2, 45}: {
		"RefService",
		"Reference Service",
		iptcString, false, true, 0, 10,
	},
	{2, 47}: {
		"RefDate",
		"Reference Date",
		iptcDate, false, true, 8, 8,
	},
	{2, 50}: {
		"RefNumber",
		"Reference Number",
		iptcString, false, true, 8, 8,
	},
	{2, 55}: {
		"DateCreated",
		"Date Created",
		iptcDate, false, false, 8, 19,
	},
	{2, 60}: {
		"TimeCreated",
		"Time Created",
		iptcTime, false, false, 6, 11,
	},
	{2, 62}: {
		"DigitalCreationDate",
		"Digital Creation Date",
		iptcDate, false, false, 8, 8,
	},
	{2, 63}: {
		"DigitalCreationTime",
		"Digital Creation Time",
		iptcTime, false, false, 11, 11,
	},
	{2, 65}: {
		"OriginatingProgram",
		"Originating Program",
		iptcString, false, false, 0, 32,
	},
	{2, 70}: {
		"ProgramVersion",
		"Program Version",
		iptcString, false, false, 0, 10,
	},
	{2, 75}: {
		"ObjectCycle",
		"Object Cycle",
		iptcString, false, false, 1, 1,
	},
	{2, 80}: {
		"Byline",
		"By-line",
		iptcString, false, true, 0, 32,
	},
	{2, 85}: {
		"BylineTitle",
		"By-line Title",
		iptcString, false, true, 0, 32,
	},
	{2, 90}: {
		"City",
		"City",
		iptcString, false, false, 0, 32,
	},
	{2, 92}: {
		"Sublocation",
		"Sub-location",
		iptcString, false, false, 0, 32,
	},
	{2, 95}: {
		"State",
		"Province/State",
		iptcString, false, false, 0, 32,
	},
	{2, 100}: {
		"CountryCode",
		"Country Code",
		iptcString, false, false, 3, 3,
	},
	{2, 101}: {
		"CountryName",
		"Country Name",
		iptcString, false, false, 0, 64,
	},
	{2, 103}: {
		"OrigTransRef",
		"Original Transmission Reference",
		iptcString, false, false, 0, 32,
	},
	{2, 105}: {
		"Headline",
		"Headline",
		iptcString, false, false, 0, 256,
	},
	{2, 110}: {
		"Credit",
		"Credit",
		iptcString, false, false, 0, 32,
	},
	{2, 115}: {
		"Source",
		"Source",
		iptcString, false, false, 0, 32,
	},
	{2, 116}: {
		"CopyrightNotice",
		"Copyright Notice",
		iptcString, false, false, 0, 128,
	},
	{2, 118}: {
		"Contact",
		"Contact",
		iptcString, false, true, 0, 128,
	},
	{2, 120}: {
		"Caption",
		"Caption/Abstract",
		iptcString, false, false, 0, 2000,
	},
	{2, 122}: {
		"WriterEditor",
		"Writer/Editor",
		iptcString, false, true, 0, 32,
	},
	{2, 125}: {
		"RasterizedCaption",
		"Rasterized Caption",
		iptcBinary, false, false, 7360, 7360,
	},
	{2, 130}: {
		"ImageType",
		"Image Type",
		iptcString, false, false, 2, 2,
	},
	{2, 131}: {
		"ImageOrientation",
		"Image Orientation",
		iptcString, false, false, 1, 1,
	},
	{2, 135}: {
		"LanguageID",
		"Language Identifier",
		iptcString, false, false, 2, 3,
	},
	{2, 150}: {
		"AudioType",
		"Audio Type",
		iptcString, false, false, 2, 2,
	},
	{2, 151}: {
		"AudioSamplingRate",
		"Audio Sampling Rate",
		iptcString, false, false, 6, 6,
	},
	{2, 152}: {
		"AudioSamplingRes",
		"Audio Sampling Resolution",
		iptcString, false, false, 2, 2,
	},
	{2, 153}: {
		"AudioDuration",
		"Audio Duration",
		iptcString, false, false, 6, 6,
	},
	{2, 154}: {
		"AudioOutcue",
		"Audio Outcue",
		iptcString, false, false, 0, 64,
	},
	{2, 200}: {
		"PreviewFileFormat",
		"Preview File Format",
		iptcShort, false, false, 2, 2,
	},
	{2, 201}: {
		"PreviewFileFormatVer",
		"Preview File Format Version",
		iptcShort, false, false, 2, 2,
	},
	{2, 202}: {
		"PreviewData",
		"Preview Data",
		iptcBinary, false, false, 0, 256000,
	},
	{7, 10}: {
		"SizeMode",
		"Size Mode",
		iptcByte, true, false, 1, 1,
	},
	{7, 20}: {
		"MaxSubfileSize",
		"Max Subfile Size",
		iptcLong, true, false, 3, 4,
	},
	{7, 90}: {
		"ObjectSizeAnnounced",
		"Object Size Announced",
		iptcLong, false, false, 3, 4,
	},
	{7, 95}: {
		"MaxObjectSize",
		"Maximum Object Size",
		iptcLong, false, false, 3, 4,
	},
	{8, 10}: {
		"Subfile",
		"Subfile",
		iptcBinary, true, true, 0, math.MaxUint32,
	},
	{9, 10}: {
		"ConfirmedDataSize",
		"Confirmed Data Size",
		iptcLong, true, false, 3, 4,
	},
}
